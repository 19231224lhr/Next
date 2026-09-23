package main

// E4 is a finite experimental cohort. Sending never waits for a paused replica;
// every replica's exact outstanding facts remain in the bounded sample array.
import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/blockfollow"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type faultSample struct {
	Index                                  int
	Request                                protocol.DirectRequest
	Certificate                            *protocol.OutputCertificate `json:",omitempty"`
	ScheduledNS, SentNS, ReadyNS, PublicNS int64
	MemberNS                               [4]int64
	Attempts                               int
	FastMS                                 float64
	Error                                  string `json:",omitempty"`
}
type faultReport struct {
	StartedNS, FinishedNS int64
	Rate                  float64
	Samples               []faultSample
	ProgressErrors        [4]int
	WalletError           string
}

func prepareFault(lab cfg.Lab, n cfg.Network, count int) error {
	owner, err := cfg.PrivateKey(lab.Owners[0])
	if err != nil {
		return err
	}
	d := protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: n.Organizations[0].Org}, owner)
	n.Genesis.Outputs = nil
	for i := 0; i < count; i++ {
		for _, asset := range []protocol.Asset{protocol.AssetCAL, protocol.AssetFUEL} {
			id := protocol.OutputID(protocol.Digest("E4_ORIGIN", n.Genesis.Network[:], []byte(fmt.Sprintf("%d/%d", asset, i))))
			amount := uint64(100)
			if asset == protocol.AssetFUEL {
				amount = 10000
			}
			n.Genesis.Outputs = append(n.Genesis.Outputs, state.OriginOutput{ID: id, Fact: protocol.Digest("E4_FINAL", id[:]), Output: protocol.Output{Asset: asset, Amount: amount, Recipient: d}})
		}
	}
	gs := n.Genesis.Grants[:0]
	for _, g := range n.Genesis.Grants {
		if g.Key.Kind != protocol.ResourceFUEL && g.Key.Kind != protocol.ResourcePolicy {
			gs = append(gs, g)
		}
	}
	n.Genesis.Grants = gs
	as := n.Accounts[:0]
	for _, a := range n.Accounts {
		if a.Asset != protocol.AssetFUEL {
			as = append(as, a)
		}
	}
	n.Accounts = as
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return os.WriteFile(lab.Network, raw, 0600)
}

func faultDirect(args []string) error {
	f := flag.NewFlagSet("fault-v4", flag.ContinueOnError)
	dir := f.String("dir", "", "fresh E4 lab")
	count := f.Int("count", 1, "finite cohort <=30000")
	rate := f.Float64("rate", 200, "planned sends/s")
	prepare := f.Bool("prepare", false, "self-funded genesis before boot")
	audit := f.Bool("audit", false, "stopped state audit")
	drain := f.Duration("drain", 60*time.Second, "observation after sends")
	timeout := f.Duration("request-timeout", 30*time.Second, "same-request retry deadline")
	mode := f.String("mode", "load", "load or conflicts")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *count < 1 || *count > 30000 || *rate <= 0 {
		return protocol.ErrRule
	}
	var lab cfg.Lab
	var n cfg.Network
	if err := cfg.Read(filepath.Join(*dir, "lab.json"), &lab); err != nil {
		return err
	}
	if err := cfg.Read(lab.Network, &n); err != nil {
		return err
	}
	if *prepare {
		return prepareFault(lab, n, *count)
	}
	if *audit {
		if *mode == "conflicts" {
			return auditFaultConflicts(*dir, lab, n)
		}
		return auditFault(*dir, lab, n)
	}
	if *mode == "conflicts" {
		return faultConflicts(*dir, lab, n, *count)
	}
	return runFaultLoad(*dir, lab, n, *count, *rate, *drain, *timeout)
}

func faultRequests(lab cfg.Lab, n cfg.Network, count int) ([]protocol.DirectRequest, error) {
	key, err := cfg.PrivateKey(lab.Owners[0])
	if err != nil {
		return nil, err
	}
	to, err := cfg.PrivateKey(lab.Owners[1])
	if err != nil {
		return nil, err
	}
	org := n.Organizations[0]
	dest := protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: org.Org}, to)
	policy, err := n.Direct.Policy(n.Schedule, n.Organizations)
	if err != nil {
		return nil, err
	}
	var cal, fuel []state.OriginOutput
	for _, o := range n.Genesis.Outputs {
		if o.Output.Asset == protocol.AssetCAL {
			cal = append(cal, o)
		} else {
			fuel = append(fuel, o)
		}
	}
	if count > len(cal) || count > len(fuel) {
		return nil, protocol.ErrRule
	}
	out := make([]protocol.DirectRequest, count)
	for i := range out {
		out[i], err = budgetRequest(n, policy, org, key, protocol.Input{Kind: protocol.FinalInput, Output: cal[i].ID, Evidence: cal[i].Fact}, cal[i].Output, dest, nil, fuel[i])
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func faultSend(ctx context.Context, client *http.Client, url string, raw []byte, retry bool) (*protocol.OutputCertificate, int, error) {
	for attempts := 1; ; attempts++ {
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(raw))
		if err != nil {
			return nil, attempts, err
		}
		req.Header.Set("Content-Type", transport.MediaType)
		resp, err := client.Do(req)
		if err == nil {
			b, e := io.ReadAll(io.LimitReader(resp.Body, protocol.MaxCertificateBytes))
			resp.Body.Close()
			if e != nil {
				err = e
			} else if resp.StatusCode == 200 {
				c, e := protocol.DecodeOutputCertificate(b)
				return &c, attempts, e
			} else {
				err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, b)
			}
		}
		if !retry || ctx.Err() != nil {
			return nil, attempts, err
		}
		if e := budgetPause(ctx, 50*time.Millisecond); e != nil {
			return nil, attempts, e
		}
	}
}

type e5LoadOptions struct {
	Offset int
	Window time.Duration
	Seed   int64
}

func runFaultLoad(dir string, lab cfg.Lab, n cfg.Network, count int, rate float64, drain, timeout time.Duration, options ...e5LoadOptions) (err error) {
	var option e5LoadOptions
	if len(options) > 0 {
		option = options[0]
	}
	requests, err := faultRequests(lab, n, count+option.Offset)
	if err != nil {
		return err
	}
	requests = requests[option.Offset:]
	if option.Seed != 0 {
		rand.New(rand.NewSource(option.Seed)).Shuffle(len(requests), func(i, j int) { requests[i], requests[j] = requests[j], requests[i] })
	}
	db, err := store.OpenNoSync(filepath.Join(dir, "fault-wallet.db"), store.Identity{Network: n.ChainID, Role: "e4-wallet", Node: "pair", Schema: 4})
	if err != nil {
		return err
	}
	group, err := store.NewGroup(db, 256, 64)
	if err != nil {
		db.Close()
		return err
	}
	defer group.Close()
	sender, err := wallet.New(group, n.Genesis.Network, requests[0].Tx.Body.Subject, n.Organizations)
	if err != nil {
		return err
	}
	receiver, err := wallet.New(group, n.Genesis.Network, requests[0].Tx.Body.Outputs[0].Recipient.Owner, n.Organizations)
	if err != nil {
		return err
	}
	raws := make([][]byte, count)
	for i, req := range requests {
		if err = sender.SaveDirectRequest(req); err != nil {
			return err
		}
		raws[i], err = req.MarshalBinary()
		if err != nil {
			return err
		}
	}
	trust, err := n.Trust()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	public := transport.NewCommitteeClient(n.CommitteeURLs[0])
	stopFollower := blockfollow.Start(ctx, cancel, group, public, trust, receiver.PrepareBlock)
	defer stopFollower()
	report := faultReport{Rate: rate, Samples: make([]faultSample, count)}
	var mu sync.Mutex
	events, err := os.Create(filepath.Join(dir, "reports", "fault-events.jsonl"))
	if err != nil {
		return err
	}
	defer events.Close()
	event := func(kind string, index int) {
		_ = json.NewEncoder(events).Encode(map[string]any{"Kind": kind, "Index": index, "NS": time.Now().UnixNano()})
	}
	for i := range report.Samples {
		report.Samples[i] = faultSample{Index: i, Request: requests[i], Error: "not sent"}
	}
	start := time.Now()
	report.StartedNS = start.UnixNano()
	if err = cfg.Write(filepath.Join(dir, "reports", "fault-start.json"), map[string]any{"StartedNS": report.StartedNS, "Count": count, "Rate": rate}); err != nil {
		return err
	}
	var observers sync.WaitGroup
	// One observer per replica, never one waiting goroutine per missing fact.
	for replica := -1; replica < 4; replica++ {
		observers.Add(1)
		go func(replica int) {
			defer observers.Done()
			cursor := 0
			client := transport.NewHTTPClient(500 * time.Millisecond)
			for ctx.Err() == nil {
				indices := []int{}
				facts := []protocol.SpendFactID{}
				mu.Lock()
				for checked := 0; checked < count && len(indices) < member.MaxProgressBatch; checked++ {
					i := cursor
					cursor = (cursor + 1) % count
					s := report.Samples[i]
					if s.Certificate == nil {
						continue
					}
					done := s.PublicNS
					if replica >= 0 {
						done = s.MemberNS[replica]
					}
					if done == 0 {
						indices = append(indices, i)
						facts = append(facts, s.Certificate.QC.Fact)
					}
				}
				mu.Unlock()
				if len(indices) > 0 {
					statuses := make([]member.DirectStatus, len(indices))
					var e error
					if replica >= 0 {
						statuses, e = loadProgressBatch(ctx, client, n.Members[n.Organizations[0].Org][replica], facts)
					} else {
						for j, i := range indices {
							ok, qerr := receiver.DirectFinal(protocol.OutputIdentity(n.Genesis.Network, report.Samples[i].Request.Tx.ID(), 0), 0)
							if qerr != nil {
								e = qerr
								break
							}
							statuses[j].Observed = ok
						}
					}
					mu.Lock()
					if e != nil {
						if replica >= 0 {
							report.ProgressErrors[replica]++
						} else {
							report.WalletError = e.Error()
						}
					} else {
						for j, i := range indices {
							if statuses[j].Observed && (replica < 0 || !statuses[j].Signed || statuses[j].Closed) {
								if replica < 0 {
									report.Samples[i].PublicNS = time.Now().UnixNano()
									event("public", i)
								} else {
									report.Samples[i].MemberNS[replica] = time.Now().UnixNano()
								}
							}
						}
					}
					mu.Unlock()
				}
				if budgetPause(ctx, 100*time.Millisecond) != nil {
					return
				}
			}
		}(replica)
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	client := transport.NewHTTPClient(2 * time.Second)
	for w := 0; w < 64; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				sent := time.Now()
				mu.Lock()
				s := &report.Samples[i]
				s.ScheduledNS = start.Add(time.Duration(float64(i) / rate * float64(time.Second))).UnixNano()
				s.SentNS = sent.UnixNano()
				s.Error = ""
				event("sent", i)
				mu.Unlock()
				call, stop := context.WithTimeout(ctx, timeout)
				cert, attempts, e := faultSend(call, client, lab.Gateways[0]+"/v3/transactions", raws[i], true)
				stop()
				if e == nil {
					e = receiver.ReceiveDirect(requests[i].Tx.Body.Outputs[0], *cert, 0)
				}
				mu.Lock()
				s = &report.Samples[i]
				s.Attempts = attempts
				if e != nil {
					s.Error = e.Error()
					event("error", i)
				} else {
					s.Certificate = cert
					s.ReadyNS = time.Now().UnixNano()
					s.FastMS = float64(time.Since(sent)) / 1e6
					event("ready", i)
				}
				mu.Unlock()
			}
		}()
	}
	sendCtx := ctx
	if option.Window > 0 {
		var stop context.CancelFunc
		sendCtx, stop = context.WithDeadline(ctx, start.Add(option.Window))
		defer stop()
	}
	for i := 0; i < count; i++ {
		due := start.Add(time.Duration(float64(i) / rate * float64(time.Second)))
		if err = budgetPause(sendCtx, time.Until(due)); err != nil {
			break
		}
		select {
		case jobs <- i:
		case <-sendCtx.Done():
			err = sendCtx.Err()
		}
		if err != nil {
			break
		}
	}
	close(jobs)
	workers.Wait()
	deadline := time.Now().Add(drain)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		complete := true
		mu.Lock()
		for _, s := range report.Samples {
			if s.ReadyNS == 0 || s.PublicNS == 0 {
				complete = false
				break
			}
			for _, v := range s.MemberNS {
				if v == 0 {
					complete = false
				}
			}
		}
		mu.Unlock()
		if complete {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	observers.Wait()
	stopFollower()
	report.FinishedNS = time.Now().UnixNano()
	file, e := os.Create(filepath.Join(dir, "reports", "fault-v4.json"))
	if e != nil {
		return e
	}
	e = json.NewEncoder(file).Encode(report)
	file.Close()
	if e != nil {
		return e
	}
	completed := 0
	for _, s := range report.Samples {
		if s.PublicNS > 0 && s.MemberNS[0] > 0 && s.MemberNS[1] > 0 && s.MemberNS[2] > 0 && s.MemberNS[3] > 0 {
			completed++
		}
	}
	fmt.Printf("E4 completed %d/%d in %.3fs\n", completed, count, float64(report.FinishedNS-report.StartedNS)/1e9)
	if completed != count {
		return fmt.Errorf("E4 incomplete: %d/%d", completed, count)
	}
	return err
}

func auditFault(dir string, lab cfg.Lab, n cfg.Network) error {
	var report faultReport
	file, err := os.Open(filepath.Join(dir, "reports", "fault-v4.json"))
	if err != nil {
		return err
	}
	defer file.Close()
	if err = json.NewDecoder(file).Decode(&report); err != nil {
		return err
	}
	type check struct {
		Fact                                    protocol.SpendFactID
		Fee                                     rules.Escrow
		Signers, ClosedSigners, ObservedMembers int
	}
	checks := make([]check, len(report.Samples))
	for i, s := range report.Samples {
		if s.Certificate == nil {
			return fmt.Errorf("sample %d has no QC", i)
		}
		checks[i].Fact = s.Certificate.QC.Fact
	}
	for _, node := range lab.Nodes {
		if node.Binary != "member" && node.Name != "committee0" {
			continue
		}
		if err := store.Inspect(filepath.Join(dir, node.Name, node.Binary+".db"), func(v state.ReadView) error {
			for i, s := range report.Samples {
				r := &checks[i]
				fact := r.Fact
				if node.Binary == "member" {
					a, found, e := state.Load[state.Approval](v, state.Key(state.KeyApproval, fact[:]))
					if e != nil {
						return e
					}
					observed, _, e := state.Load[bool](v, state.Key(state.KeyObserved, fact[:]))
					if e != nil {
						return e
					}
					if observed {
						r.ObservedMembers++
					}
					if found {
						r.Signers++
						applied, e := member.AppliedDebits(v, a)
						if e != nil {
							return e
						}
						closed := true
						for j, d := range a.Debits {
							if applied[j] != d.Cap {
								closed = false
							}
						}
						if closed {
							r.ClosedSigners++
						}
					}
					continue
				}
				p, found, e := state.Load[rules.DirectPaymentState](v, state.Key(103, fact[:]))
				if e != nil {
					return e
				}
				if !found || p.FeeSource != protocol.OwnerFinalUTXO || !p.Fee.Closed {
					return fmt.Errorf("payment missing or fee incomplete %d", i)
				}
				r.Fee = p.Fee
				for _, in := range append(s.Request.Tx.Body.Inputs, s.Request.Tx.Body.Fee.Inputs...) {
					spent, _, e := state.Load[state.Spend](v, rules.DirectSpendKey(in.Output, 0))
					if e != nil {
						return e
					}
					if spent.Consumed != fact {
						return fmt.Errorf("input not consumed by expected fact %d", i)
					}
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for i, r := range checks {
		if r.Signers < 3 || r.Signers != r.ClosedSigners || r.ObservedMembers != 4 {
			return fmt.Errorf("member audit failed %d: %+v", i, r)
		}
	}
	return cfg.Write(filepath.Join(dir, "reports", "fault-audit.json"), checks)
}
