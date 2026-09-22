package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"flag"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/blockfollow"
	"utxo/internal/gateway"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type budgetUnit struct {
	Index                                int
	PlannedUnixNS, StartedUnixNS         int64
	Parent, Child                        chainHop
	ReleaseAtUnixNS, FirstSubmitUnixNS   int64
	SubmitAttempts                       int
	FirstSubmitDelayNS                   int64
	Replays                              []string `json:",omitempty"`
	ParentError, ChildError, SubmitError string   `json:",omitempty"`
	ParentFailures, ChildFailures        map[string]int
}
type budgetReport struct {
	StartedUnixNS, StoppedUnixNS              int64
	DurationSeconds, DelaySeconds             float64
	Offered, Admitted, NotStarted, MaxPending int
	Units                                     []*budgetUnit
	ParentFinalInstance                       uint8
}

func budgetPause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(max(d, 0))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Parent publication belongs to the experiment lifecycle, never the child
// attempt. The fixed deadline survives retries; only the report/drain deadline
// can terminate it. HTTP acceptance is followed separately by wallet finality.
func budgetRelease(ctx context.Context, at time.Time, submit func(context.Context) error, onAttempt func()) error {
	if e := budgetPause(ctx, time.Until(at)); e != nil {
		return e
	}
	for {
		onAttempt()
		e := submit(ctx)
		if e == nil {
			return nil
		}
		if e = budgetPause(ctx, 200*time.Millisecond); e != nil {
			return e
		}
	}
}

func budgetRequest(n cfg.Network, p rules.DirectPolicy, org protocol.OrgConfig, key ed25519.PrivateKey, in protocol.Input, coin protocol.Output, to protocol.ReceiveDescriptor, proofs []protocol.InputCertificate) (protocol.DirectRequest, error) {
	body := protocol.TxBody{Wire: 4, Version: 4, Network: n.Genesis.Network, Kind: protocol.FastTransfer, Subject: coin.Recipient.Owner, Certifier: org.Org, Config: org.Hash(), Epoch: org.Epoch, Rules: p.Rules(), Inputs: []protocol.Input{in}, Outputs: []protocol.Output{{Asset: protocol.AssetCAL, Amount: coin.Amount, Recipient: to}}, Fee: protocol.FeeTerms{Source: protocol.OrgReserve, Account: org.Org, Version: 1, Maximum: 1000}, Work: protocol.WorkLimit{Execution: 100000, Bytes: 10000000, Depth: 1, Ancestors: 1}}
	for _, g := range n.Genesis.Grants {
		if g.Organization != org.Hash() || (g.Key.Kind == protocol.ResourcePolicy && g.Subject != body.Subject) {
			continue
		}
		body.Admission = append(body.Admission, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
		if g.Key.Kind == protocol.ResourcePolicy {
			body.Fee.Policy = g.Key.Account
		}
	}
	nonce := protocol.Digest("E2_BUDGET", in.Output[:])
	copy(body.Nonce[:], nonce[:])
	body.Intent = body.IntentID()
	tx, e := protocol.NewFastTx(body, []protocol.InputClaim{{Output: coin}}, p.Key)
	if e != nil {
		return protocol.DirectRequest{}, e
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), key)}
	return protocol.DirectRequest{Tx: tx, InputCertificates: proofs}, nil
}

func budgetDirect(args []string) error {
	f := flag.NewFlagSet("budget-v4", flag.ContinueOnError)
	dir := f.String("dir", "", "fresh E2 lab")
	duration := f.Duration("duration", 300*time.Second, "fixed admission window")
	delay := f.Duration("parent-delay", time.Second, "fixed READY to parent publication delay")
	drain := f.Duration("drain", 60*time.Second, "bounded drain without topping up")
	rate := f.Int("rate", 20, "independent two-payment units per second")
	count := f.Int("count", 0, "optional single-probe unit limit")
	pending := f.Int("pending", 64, "maximum admitted unfinished units")
	gatewayParent := f.Bool("gateway-parent", false, "obtain parent QC through the same gateway without public delivery")
	late := f.Bool("late-parent", false, "fee audit: observe late parent output instance one")
	replay := f.Bool("replay", false, "fee audit: replay the identical completed child three times")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *dir == "" || *duration <= 0 || *delay < 0 || *drain <= 0 || *rate < 1 || *rate > 100 || *pending < 1 || *pending > 256 || *count < 0 {
		return protocol.ErrRule
	}
	var lab cfg.Lab
	var n cfg.Network
	if e := cfg.Read(filepath.Join(*dir, "lab.json"), &lab); e != nil {
		return e
	}
	if e := cfg.Read(lab.Network, &n); e != nil {
		return e
	}
	p, e := n.Direct.Policy(n.Schedule, n.Organizations)
	if e != nil {
		return e
	}
	trust, e := n.Trust()
	if e != nil {
		return e
	}
	org := n.Organizations[0]
	var keys [3]ed25519.PrivateKey
	var desc [3]protocol.ReceiveDescriptor
	var wallets [3]*wallet.Wallet
	var dbs [3]*store.Group
	for i, path := range []string{lab.Owners[0], lab.Owners[1], filepath.Join(*dir, "keys", "chain-c.key")} {
		keys[i], e = cfg.PrivateKey(path)
		if e != nil {
			return e
		}
		desc[i] = protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: org.Org}, keys[i])
		db, err := store.OpenNoSync(filepath.Join(*dir, fmt.Sprintf("budget-wallet-%d.db", i)), store.Identity{Network: n.ChainID, Role: "budget-wallet", Node: fmt.Sprint(i), Schema: 4})
		if err != nil {
			return err
		}
		g, err := store.NewGroup(db, 256, 64)
		if err != nil {
			db.Close()
			return err
		}
		dbs[i] = g
		defer g.Close()
		wallets[i], err = wallet.New(g, n.Genesis.Network, desc[i].Owner, n.Organizations)
		if err != nil {
			return err
		}
	}
	var origins []state.OriginOutput
	for _, o := range n.Genesis.Outputs {
		if o.Output.Recipient.Owner == desc[0].Owner {
			origins = append(origins, o)
		}
	}
	total := int(duration.Seconds() * float64(*rate))
	if *count > 0 {
		total = min(total, *count)
	}
	if total < 1 || total > len(origins) {
		return fmt.Errorf("need %d independent origins, have %d", total, len(origins))
	}
	start := time.Now()
	ctx, cancel := context.WithDeadline(context.Background(), start.Add(*duration+*drain))
	defer cancel()
	client := transport.NewHTTPClient(5 * time.Second)
	public := transport.NewCommitteeClient(n.CommitteeURLs[0], n.CommitteeURLs[1:]...)
	for i := range wallets {
		defer blockfollow.Start(ctx, cancel, dbs[i], transport.NewCommitteeClient(n.CommitteeURLs[0]), trust, wallets[i].PrepareBlock)()
	}
	var members [4]gateway.MemberClient
	var batches [4]*progressBatcher
	batchCtx, stopBatches := context.WithCancel(ctx)
	for i, url := range n.Members[org.Org] {
		members[i] = transport.NewMemberClient(url)
		batches[i] = newProgressBatcher(batchCtx, *pending*2, func(ctx context.Context, facts []protocol.SpendFactID) ([]member.DirectStatus, error) {
			return loadProgressBatch(ctx, client, url, facts)
		})
	}
	defer func() {
		stopBatches()
		for _, b := range batches {
			<-b.done
		}
	}()
	collector, e := gateway.New(org, members, dbs[0])
	if e != nil {
		return e
	}
	report := budgetReport{StartedUnixNS: start.UnixNano(), DurationSeconds: duration.Seconds(), DelaySeconds: delay.Seconds(), Offered: total, MaxPending: *pending}
	if *late {
		report.ParentFinalInstance = 1
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, *pending)
	for index := 0; index < total; index++ {
		planned := start.Add(time.Duration(index) * time.Second / time.Duration(*rate))
		if e = budgetPause(ctx, time.Until(planned)); e != nil {
			break
		}
		if time.Now().After(start.Add(*duration)) {
			break
		}
		select {
		case slots <- struct{}{}:
		default:
			continue
		}
		u := &budgetUnit{Index: index, PlannedUnixNS: planned.UnixNano(), StartedUnixNS: time.Now().UnixNano(), ParentFailures: map[string]int{}, ChildFailures: map[string]int{}}
		report.Units = append(report.Units, u)
		wg.Add(1)
		go func(origin state.OriginOutput, u *budgetUnit) {
			defer wg.Done()
			defer func() { <-slots }()
			parent, err := budgetRequest(n, p, org, keys[0], protocol.Input{Kind: protocol.FinalInput, Output: origin.ID, Evidence: origin.Fact}, origin.Output, desc[1], nil)
			if err != nil {
				u.ParentError = err.Error()
				return
			}
			u.Parent = chainHop{Hop: 1, Sender: 0, Receiver: 1, Input: origin.ID, Tx: parent.Tx.ID(), OutputData: parent.Tx.Body.Outputs[0], SentUnixNS: time.Now().UnixNano()}
			vector, err := rules.PrepareDirectVector(parent.Tx, p)
			if err != nil {
				u.ParentError = err.Error()
				return
			}
			u.Parent.Fact = protocol.SummaryFor(parent.Tx, vector).Fact()
			if err = wallets[0].SaveDirectRequest(parent); err != nil {
				u.ParentError = err.Error()
				return
			}
			var pc protocol.OutputCertificate
			for {
				u.Parent.Attempts++
				if *gatewayParent {
					var encoded []byte
					encoded, err = parent.MarshalBinary()
					if err == nil {
						pc, err = budgetSend(ctx, client, lab.Gateways[0]+"/debug/budget/collect", encoded)
					}
				} else {
					pc, err = collector.CollectDirect(ctx, parent)
				}
				if err == nil {
					break
				}
				u.ParentFailures[err.Error()]++
				if err = budgetPause(ctx, 200*time.Millisecond); err != nil {
					u.ParentError = err.Error()
					return
				}
			}
			if err = wallets[1].ReceiveDirect(u.Parent.OutputData, pc, 0); err != nil {
				u.ParentError = err.Error()
				return
			}
			ready := time.Now()
			u.Parent.ReadyUnixNS = ready.UnixNano()
			u.Parent.Fact = pc.QC.Fact
			u.Parent.Output = pc.Summary.OutputID(0)
			u.Parent.FastMS = float64(ready.UnixNano()-u.Parent.SentUnixNS) / 1e6
			raw, err := (protocol.DirectPayment{Tx: parent.Tx, Certificate: pc}).Submission().MarshalBinary()
			if err != nil {
				u.ParentError = err.Error()
				return
			}
			u.ReleaseAtUnixNS = ready.Add(*delay).UnixNano()
			var background sync.WaitGroup
			background.Add(1)
			go func() {
				defer background.Done()
				err := budgetRelease(ctx, ready.Add(*delay), func(ctx context.Context) error { return public.Submit(ctx, raw) }, func() {
					u.SubmitAttempts++
					if u.FirstSubmitUnixNS == 0 {
						sent := time.Now()
						u.FirstSubmitUnixNS = sent.UnixNano()
						u.FirstSubmitDelayNS = sent.Sub(ready).Nanoseconds()
					}
				})
				if err != nil {
					u.SubmitError = err.Error()
					return
				}
				// A received HTTP202 does not disable future supplementation. Keep
				// replaying the identical submission until verified wallet finality.
				retryCtx, stopRetry := context.WithCancel(ctx)
				retryDone := make(chan struct{})
				go func() {
					defer close(retryDone)
					for budgetPause(retryCtx, 2*time.Second) == nil {
						final, _ := wallets[1].DirectFinal(u.Parent.Output, report.ParentFinalInstance)
						if final {
							return
						}
						u.SubmitAttempts++
						_ = public.Submit(retryCtx, raw)
					}
				}()
				if err = observeChainHop(ctx, batches, wallets[1], &u.Parent, start, make(chan struct{}), report.ParentFinalInstance); err != nil {
					u.ParentError = err.Error()
				}
				stopRetry()
				<-retryDone
			}()
			defer background.Wait()
			// Read the real received coin. At construction time the withheld parent
			// must still be a certificate input, otherwise this is not an E2 unit.
			var coin wallet.DirectCoin
			err = dbs[1].View(func(v state.ReadView) error {
				var found bool
				var e error
				coin, found, e = state.Load[wallet.DirectCoin](v, wallet.DirectCoinKey(u.Parent.Output, 0))
				if e != nil {
					return e
				}
				if !found {
					return state.ErrNotFound
				}
				return nil
			})
			if err != nil {
				u.ChildError = err.Error()
				return
			}
			in, proofs, err := chainInput(u.Parent.Output, coin)
			if err != nil {
				u.ChildError = err.Error()
				return
			}
			child, err := budgetRequest(n, p, org, keys[1], in, coin.Output, desc[2], proofs)
			if err != nil {
				u.ChildError = err.Error()
				return
			}
			if err = wallets[1].SaveDirectRequest(child); err != nil {
				u.ChildError = err.Error()
				return
			}
			childRaw, err := child.MarshalBinary()
			if err != nil {
				u.ChildError = err.Error()
				return
			}
			u.Child = chainHop{Hop: 2, Sender: 1, Receiver: 2, Input: in.Output, Tx: child.Tx.ID(), OutputData: child.Tx.Body.Outputs[0], CertificateInput: in.Kind == protocol.CertificateInput, SentUnixNS: time.Now().UnixNano()}
			vector, err = rules.PrepareDirectVector(child.Tx, p)
			if err != nil {
				u.ChildError = err.Error()
				return
			}
			u.Child.Fact = protocol.SummaryFor(child.Tx, vector).Fact()
			var cc protocol.OutputCertificate
			for {
				u.Child.Attempts++
				cc, err = budgetSend(ctx, client, lab.Gateways[0]+"/v3/transactions", childRaw)
				if err == nil {
					break
				}
				u.ChildFailures[err.Error()]++
				if err = budgetPause(ctx, 200*time.Millisecond); err != nil {
					u.ChildError = err.Error()
					return
				}
			}
			if err = wallets[2].ReceiveDirect(u.Child.OutputData, cc, 0); err != nil {
				u.ChildError = err.Error()
				return
			}
			u.Child.ReadyUnixNS = time.Now().UnixNano()
			u.Child.Fact = cc.QC.Fact
			u.Child.Output = cc.Summary.OutputID(0)
			u.Child.FastMS = float64(u.Child.ReadyUnixNS-u.Child.SentUnixNS) / 1e6
			if err = observeChainHop(ctx, batches, wallets[2], &u.Child, start, make(chan struct{})); err != nil {
				u.ChildError = err.Error()
			}
			if *replay && err == nil {
				b, e := (protocol.DirectPayment{Tx: child.Tx, Certificate: cc, InputCertificates: child.InputCertificates}).Submission().MarshalBinary()
				if e != nil {
					u.ChildError = e.Error()
					return
				}
				for i := 0; i < 3; i++ {
					replayCtx, stop := context.WithTimeout(ctx, 500*time.Millisecond)
					e = public.Submit(replayCtx, b)
					stop()
					s := "accepted"
					if e != nil {
						s = e.Error()
					}
					u.Replays = append(u.Replays, s)
				}
			}
		}(origins[index], u)
	}
	wg.Wait()
	report.StoppedUnixNS = time.Now().UnixNano()
	report.Admitted = len(report.Units)
	report.NotStarted = report.Offered - report.Admitted
	if e = cfg.Write(filepath.Join(*dir, "reports", "budget-v4.json"), report); e != nil {
		return e
	}
	completed := 0
	for _, u := range report.Units {
		if u.Parent.MemberClosedUnixNS > 0 && u.Child.MemberClosedUnixNS > 0 {
			completed++
		}
	}
	fmt.Printf("E2 offered=%d admitted=%d closed_units=%d not_started=%d\n", report.Offered, report.Admitted, completed, report.NotStarted)
	return nil
}

func budgetSend(ctx context.Context, client *http.Client, url string, raw []byte) (protocol.OutputCertificate, error) {
	req, e := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(raw))
	if e != nil {
		return protocol.OutputCertificate{}, e
	}
	req.Header.Set("Content-Type", transport.MediaType)
	resp, e := client.Do(req)
	if e != nil {
		return protocol.OutputCertificate{}, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, protocol.MaxCertificateBytes))
	if e != nil {
		return protocol.OutputCertificate{}, e
	}
	if resp.StatusCode != http.StatusOK {
		return protocol.OutputCertificate{}, fmt.Errorf("HTTP %d %s", resp.StatusCode, bytes.TrimSpace(b))
	}
	return protocol.DecodeOutputCertificate(b)
}
