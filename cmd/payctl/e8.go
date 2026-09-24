package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/member"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type e8Options struct {
	Wallets, Capacity, Lanes, Pending, Warm int
	Rate                                    int
	Duration, Drain                         time.Duration
	Chain, Surge                            bool
	Seed                                    int
}
type e8Fixture struct{ Wallets, Capacity, Seed int }
type e8Sample struct {
	Index, Lane, Hop, Sender, Receiver, Parent                          int
	Input, Fee, Output                                                  protocol.OutputID
	Tx                                                                  protocol.TxID
	Fact                                                                protocol.SpendFactID
	CertificateInput                                                    bool
	ScheduledNS, BuildNS, SentNS, ReceivedNS, ReadyNS, PublicNS, Height int64
	MemberNS                                                            [4]int64
	Missing                                                             int
	RequestBytes, CertificateBytes, Attempts                            int
	Error                                                               string `json:",omitempty"`
}
type e8Report struct {
	Options                                                    e8Options
	StartedNS, EndSendNS, FinishedNS                           int64
	Scheduled, SkippedPacing, NoReady, PendingFull, WorkerFull int
	Samples                                                    []*e8Sample
	Descriptor                                                 protocol.DescriptorCounters
	ProgressErrors                                             int
}

func e8NextSlot(due, now time.Time, interval time.Duration) (time.Time, int) {
	next := due.Add(interval)
	missed := 0
	if !next.After(now) {
		missed = int(now.Sub(next)/interval) + 1
		next = next.Add(time.Duration(missed) * interval)
	}
	return next, missed
}

func e8Command(args []string) error {
	f := flag.NewFlagSet("e8", flag.ContinueOnError)
	dir := f.String("dir", "", "fresh lab")
	prepare := f.Bool("prepare", false, "create equal-sized final CAL/FUEL pools")
	audit := f.Bool("audit", false, "audit stopped snapshots")
	o := e8Options{}
	f.IntVar(&o.Wallets, "wallets", 2048, "logical identities")
	f.IntVar(&o.Capacity, "capacity", 160000, "planned transaction capacity including warmup")
	f.IntVar(&o.Lanes, "lanes", 64, "ready chains and fast workers")
	f.IntVar(&o.Pending, "pending", 4096, "all unfinished transactions bound")
	f.IntVar(&o.Warm, "warm", 2048, "same-process warmup payments")
	f.IntVar(&o.Rate, "rate", 500, "payment slots/s")
	f.IntVar(&o.Seed, "seed", 23, "recipient rotation offset")
	f.DurationVar(&o.Duration, "duration", 300*time.Second, "fixed send window")
	f.DurationVar(&o.Drain, "drain", 60*time.Second, "observe only after send window")
	f.BoolVar(&o.Chain, "chain", false, "ten-hop dependent chains")
	f.BoolVar(&o.Surge, "surge", false, "60s base,15s twice rate,120s base")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *dir == "" || o.Wallets < 2 || o.Wallets > 2048 || o.Capacity < 1 || o.Capacity > 1000000 || o.Rate < 1 || o.Rate > 10000 || o.Lanes < 1 || o.Lanes > 256 || o.Pending < o.Lanes || o.Duration <= 0 || o.Drain <= 0 {
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
	if *prepare {
		return prepareE8(*dir, lab, n, o)
	}
	if *audit {
		return auditE8(*dir, lab, n)
	}
	var fixture e8Fixture
	if e := cfg.Read(filepath.Join(*dir, "e8-fixture.json"), &fixture); e != nil {
		return e
	}
	o.Wallets, o.Capacity = fixture.Wallets, fixture.Capacity
	if o.Surge {
		o.Duration = 195 * time.Second
	}
	required := int(o.Duration.Seconds()*float64(o.Rate)) + o.Warm
	if o.Surge {
		required += 15 * o.Rate
	}
	if required > o.Capacity {
		return fmt.Errorf("fixture capacity %d < planned %d", o.Capacity, required)
	}
	return runE8(*dir, lab, n, o)
}

func prepareE8(dir string, lab cfg.Lab, n cfg.Network, o e8Options) error {
	keys := filepath.Join(dir, "keys", "e8")
	if e := os.Mkdir(keys, 0700); e != nil {
		return e
	}
	ds := make([]protocol.ReceiveDescriptor, o.Wallets)
	for i := range ds {
		_, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(keys, fmt.Sprintf("%d.key", i)), key, 0600); e != nil {
			return e
		}
		ds[i] = protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: n.Organizations[0].Org}, key)
	}
	n.Genesis.Outputs = nil
	// Round both cohorts to the same 2048-entry boundary; identities do not alter pool size.
	total := ((o.Capacity+2047)/2048 + 2) * 2048
	for i := 0; i < total; i++ {
		for _, asset := range []protocol.Asset{protocol.AssetCAL, protocol.AssetFUEL} {
			id := protocol.OutputID(protocol.Digest("E8_ORIGIN", n.Genesis.Network[:], []byte(fmt.Sprintf("%d/%d", asset, i))))
			amount := uint64(100)
			if asset == protocol.AssetFUEL {
				amount = 10000
			}
			n.Genesis.Outputs = append(n.Genesis.Outputs, state.OriginOutput{ID: id, Fact: protocol.Digest("E8_FINAL", id[:]), Output: protocol.Output{Asset: asset, Amount: amount, Recipient: ds[i%len(ds)]}})
		}
	}
	gs := n.Genesis.Grants[:0]
	for _, g := range n.Genesis.Grants {
		if g.Key.Kind != protocol.ResourceFUEL && g.Key.Kind != protocol.ResourcePolicy {
			gs = append(gs, g)
		}
	}
	n.Genesis.Grants = gs
	accounts := n.Accounts[:0]
	for _, a := range n.Accounts {
		if a.Asset != protocol.AssetFUEL {
			accounts = append(accounts, a)
		}
	}
	n.Accounts = accounts
	raw, e := json.Marshal(n)
	if e != nil {
		return e
	}
	if e = os.WriteFile(lab.Network, raw, 0600); e != nil {
		return e
	}
	return cfg.Write(filepath.Join(dir, "e8-fixture.json"), e8Fixture{o.Wallets, o.Capacity, o.Seed})
}

type e8Lane struct {
	ID                 protocol.OutputID
	Owner, Hop, Parent int
	Busy               bool
}

func e8RootOwner(chain bool, lane e8Lane, recipient, wallets int) int {
	// A new independent root belongs to the preceding chain's last recipient.
	// This balances owner-paid fee inputs without spending the old chain tip.
	if chain && lane.Owner >= 0 { return lane.Owner }
	return (recipient+wallets-1)%wallets
}
type e8Job struct {
	s      *e8Sample
	lane   e8Lane
	fee    state.OriginOutput
	origin *state.OriginOutput
}

func runE8(dir string, lab cfg.Lab, n cfg.Network, o e8Options) (err error) {
	keys := make([]ed25519.PrivateKey, o.Wallets)
	ds := make([]protocol.ReceiveDescriptor, o.Wallets)
	owners := map[protocol.PublicKey]bool{}
	indices := map[protocol.PublicKey]int{}
	for i := range keys {
		keys[i], err = cfg.PrivateKey(filepath.Join(dir, "keys", "e8", fmt.Sprintf("%d.key", i)))
		if err != nil {
			return err
		}
		ds[i] = protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: n.Organizations[0].Org}, keys[i])
		owners[ds[i].Owner] = true
		indices[ds[i].Owner] = i
	}
	cal, fuel := make([][]state.OriginOutput, o.Wallets), make([][]state.OriginOutput, o.Wallets)
	for _, v := range n.Genesis.Outputs {
		i, ok := indices[v.Output.Recipient.Owner]
		if !ok {
			continue
		}
		if v.Output.Asset == protocol.AssetCAL {
			cal[i] = append(cal[i], v)
		} else {
			fuel[i] = append(fuel[i], v)
		}
	}
	ci, fi := make([]int, o.Wallets), make([]int, o.Wallets)
	db, e := store.OpenNoSync(filepath.Join(dir, "e8-wallet.db"), store.Identity{Network: n.ChainID, Role: "e8-wallet", Node: "shared", Schema: 4})
	if e != nil {
		return e
	}
	group, e := store.NewGroup(db, 256, 64)
	if e != nil {
		db.Close()
		return e
	}
	defer group.Close()
	ws := make([]*wallet.Wallet, o.Wallets)
	for i := range ws {
		ws[i], e = wallet.New(group, n.Genesis.Network, ds[i].Owner, n.Organizations)
		if e != nil {
			return e
		}
	}
	policy, e := n.Direct.Policy(n.Schedule, n.Organizations)
	if e != nil {
		return e
	}
	trust, e := n.Trust()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	pending := map[int]*e8Sample{}
	locations := map[protocol.TxID]struct {
		Height  int64
		Missing int
	}{}
	var blockRows []struct {
		Tx      protocol.TxID
		Height  int64
		Missing int
	}
	prepare := wallet.PrepareOwnersBlock(n.Genesis.Network, owners)
	wrapped := func(b finality.VerifiedBlock) (blockfollow.Apply, error) {
		apply, e := prepare(b)
		if e != nil {
			return nil, e
		}
		blockRows = nil
		for _, entry := range b.Transactions() {
			if entry.Code != 0 || protocol.IsRepairInput(entry.Bytes) || len(entry.Data) == 0 {
				continue
			}
			r, e := protocol.DecodeExecution(entry.Data)
			if e != nil {
				return nil, e
			}
			if !r.Applied {
				continue
			}
			p, e := protocol.DecodeDirectSubmission(entry.Bytes)
			if e != nil {
				return nil, e
			}
			blockRows = append(blockRows, struct {
				Tx      protocol.TxID
				Height  int64
				Missing int
			}{p.Tx.ID(), b.Height(), len(r.MissingInputs)})
		}
		return apply, nil
	}
	stopFollow := blockfollow.Start(ctx, cancel, group, transport.NewCommitteeClient(n.CommitteeURLs[0]), trust, wrapped, func(_ int64) {
		mu.Lock()
		defer mu.Unlock()
		for _, r := range blockRows {
			locations[r.Tx] = struct {
				Height  int64
				Missing int
			}{r.Height, r.Missing}
		}
	})
	defer stopFollow()
	progressErrors := 0
	var observers sync.WaitGroup
	for replica := 0; replica < 4; replica++ {
		observers.Add(1)
		go func(replica int) {
			defer observers.Done()
			client := transport.NewHTTPClient(2 * time.Second)
			for ctx.Err() == nil {
				mu.Lock()
				todo := make([]*e8Sample, 0, len(pending))
				for _, s := range pending {
					if s.ReadyNS > 0 && s.MemberNS[replica] == 0 {
						todo = append(todo, s)
					}
				}
				mu.Unlock()
				for start := 0; start < len(todo); start += member.MaxProgressBatch {
					batch := todo[start:min(start+member.MaxProgressBatch, len(todo))]
					facts := make([]protocol.SpendFactID, len(batch))
					for i, s := range batch {
						facts[i] = s.Fact
					}
					statuses, e := loadProgressBatch(ctx, client, n.Members[n.Organizations[0].Org][replica], facts)
					mu.Lock()
					if e != nil {
						progressErrors++
					} else {
						for i, s := range batch {
							if statuses[i].Observed && (!statuses[i].Signed || statuses[i].Closed) {
								s.MemberNS[replica] = time.Now().UnixNano()
							}
						}
					}
					mu.Unlock()
				}
				if budgetPause(ctx, 50*time.Millisecond) != nil {
					return
				}
			}
		}(replica)
	}
	defer func() { cancel(); observers.Wait() }()
	report := e8Report{Options: o}
	jobs := make(chan e8Job, o.Lanes)
	done := make(chan *e8Sample, o.Lanes)
	var workers sync.WaitGroup
	for i := 0; i < o.Lanes; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			client := transport.NewHTTPClient(3 * time.Second)
			for j := range jobs {
				s := j.s
				s.BuildNS = time.Now().UnixNano()
				var coin wallet.DirectCoin
				if j.origin != nil {
					coin = wallet.DirectCoin{Output: j.origin.Output, Final: j.origin.Fact}
				} else {
					e := group.View(func(v state.ReadView) error {
						c, ok, e := state.Load[wallet.DirectCoin](v, wallet.DirectCoinKey(j.lane.ID, 0))
						coin = c
						if !ok && e == nil {
							return state.ErrNotFound
						}
						return e
					})
					if e != nil {
						s.Error = e.Error()
						done <- s
						continue
					}
				}
				in, proofs, e := chainInput(j.lane.ID, coin)
				s.CertificateInput = in.Kind == protocol.CertificateInput
				var req protocol.DirectRequest
				if e == nil {
					req, e = budgetRequest(n, policy, n.Organizations[0], keys[s.Sender], in, coin.Output, ds[s.Receiver], proofs, j.fee)
				}
				if e == nil {
					e = ws[s.Sender].SaveDirectRequest(req)
				}
				var raw []byte
				if e == nil {
					raw, e = req.MarshalBinary()
				}
				if e == nil {
					s.Tx = req.Tx.ID()
					s.Output = protocol.OutputIdentity(n.Genesis.Network, s.Tx, 0)
					s.RequestBytes = len(raw)
					if time.Now().UnixNano() >= report.EndSendNS && report.EndSendNS != 0 {
						s.Error = "not sent: window ended"
						done <- s
						continue
					}
					s.SentNS = time.Now().UnixNano()
					call, stop := context.WithTimeout(ctx, 10*time.Second)
					encoded, _, attempts, sendErr := submitChain(call, client, lab.Gateways[0]+"/v3/transactions", raw, false)
					stop()
					s.Attempts = attempts
					s.ReceivedNS = time.Now().UnixNano()
					e = sendErr
					if e == nil {
						var cert protocol.OutputCertificate
						cert, e = protocol.DecodeOutputCertificate(encoded)
						if e == nil {
							s.Fact = cert.QC.Fact
							s.CertificateBytes = len(encoded)
							e = ws[s.Receiver].ReceiveDirect(req.Tx.Body.Outputs[0], cert, 0)
						}
					}
				}
				if e != nil {
					s.Error = e.Error()
				} else {
					s.ReadyNS = time.Now().UnixNano()
				}
				mu.Lock()
				pending[s.Index] = s
				mu.Unlock()
				done <- s
			}
		}()
	}
	// All sample mutation by observers occurs under mu; workers publish only after receipt.
	lanes := make([]e8Lane, o.Lanes)
	for i := range lanes {
		lanes[i].Parent = -1
		lanes[i].Owner = -1
	}
	rotating := o.Seed % o.Wallets
	cursor := 0
	inflight := 0
	eventFile, e := os.Create(filepath.Join(dir, "reports", "e8-timeline.jsonl"))
	if e != nil {
		return e
	}
	defer eventFile.Close()
	encoder := json.NewEncoder(eventFile)
	retire := func() {
		mu.Lock()
		defer mu.Unlock()
		for i, s := range pending {
			if loc, ok := locations[s.Tx]; ok && s.PublicNS == 0 {
				s.PublicNS = time.Now().UnixNano()
				s.Height = loc.Height
				s.Missing = loc.Missing
			}
			if s.PublicNS > 0 && s.MemberNS[0] > 0 && s.MemberNS[1] > 0 && s.MemberNS[2] > 0 && s.MemberNS[3] > 0 {
				delete(pending, i)
			}
		}
	}
	consume := func(s *e8Sample) {
		inflight--
		l := &lanes[s.Lane]
		l.Busy = false
		if s.ReadyNS > 0 && o.Chain && s.Hop < 10 {
			l.ID = s.Output
			l.Owner = s.Receiver
			l.Hop = s.Hop
			l.Parent = s.Index
		} else {
			l.ID = protocol.OutputID{}
			l.Hop = 0
			l.Parent = -1
			l.Owner = -1
			if s.ReadyNS > 0 { l.Owner = s.Receiver }
		}
	}
	start := time.Now()
	report.StartedNS = start.UnixNano()
	warmDuration := time.Duration(float64(o.Warm) / 200 * float64(time.Second))
	end := start.Add(warmDuration + o.Duration)
	report.EndSendNS = end.UnixNano()
	if e = cfg.Write(filepath.Join(dir, "reports", "e8-start.json"), map[string]any{"StartedNS": report.StartedNS, "FormalNS": start.Add(warmDuration).UnixNano(), "EndSendNS": report.EndSendNS, "Options": o}); e != nil {
		return e
	}
	due := start
	lastSample := start
	nextIndex := 0
	for time.Now().Before(end) && ctx.Err() == nil {
		for inflight > 0 {
			select {
			case s := <-done:
				consume(s)
			default:
				goto emptied
			}
		}
	emptied:
		retire()
		now := time.Now()
		if now.Sub(lastSample) >= time.Second {
			mu.Lock()
			oldest := int64(0)
			for _, s := range pending {
				if s.SentNS > 0 && (oldest == 0 || s.SentNS < oldest) {
					oldest = s.SentNS
				}
			}
			_ = encoder.Encode(map[string]any{"NS": now.UnixNano(), "Dispatched": nextIndex, "Pending": len(pending), "InFlight": inflight, "OldestNS": oldest, "Skipped": report.SkippedPacing, "NoReady": report.NoReady, "PendingFull": report.PendingFull})
			mu.Unlock()
			lastSample = now
		}
		if now.Before(due) {
			select {
			case s := <-done:
				consume(s)
			case <-time.After(min(time.Until(due), 10*time.Millisecond)):
			case <-ctx.Done():
			}
			continue
		}
		rate := o.Rate
		if now.Before(start.Add(warmDuration)) {
			rate = 200
		} else if o.Surge {
			elapsed := now.Sub(start.Add(warmDuration))
			if elapsed >= 60*time.Second && elapsed < 75*time.Second {
				rate *= 2
			}
		}
		interval := time.Second / time.Duration(rate)
		scheduled := due
		var missed int
		due, missed = e8NextSlot(due, now, interval)
		report.SkippedPacing += missed
		report.Scheduled += 1 + missed
		mu.Lock()
		busy := len(pending)+inflight >= o.Pending
		mu.Unlock()
		if busy {
			report.PendingFull++
			continue
		}
		chosen := -1
		for k := 0; k < len(lanes); k++ {
			i := (cursor + k) % len(lanes)
			if !lanes[i].Busy {
				chosen = i
				cursor = (i + 1) % len(lanes)
				break
			}
		}
		if chosen < 0 {
			report.NoReady++
			continue
		}
		l := lanes[chosen]
		var origin *state.OriginOutput
		to := rotating
		rotating = (rotating + 1) % o.Wallets
		if !o.Chain || l.ID == (protocol.OutputID{}) {
			owner := e8RootOwner(o.Chain,l,to,o.Wallets)
			if ci[owner] >= len(cal[owner]) {
				err = fmt.Errorf("CAL pool exhausted owner %d", owner)
				break
			}
			origin = &cal[owner][ci[owner]]
			ci[owner]++
			l = e8Lane{ID: origin.ID, Owner: owner, Parent: -1}
		}
		if to == l.Owner {
			to = (to + 1) % o.Wallets
		}
		if fi[l.Owner] >= len(fuel[l.Owner]) {
			err = fmt.Errorf("FUEL pool exhausted owner %d", l.Owner)
			break
		}
		fee := fuel[l.Owner][fi[l.Owner]]
		fi[l.Owner]++
		s := &e8Sample{Index: nextIndex, Lane: chosen, Hop: l.Hop + 1, Sender: l.Owner, Receiver: to, Parent: l.Parent, Input: l.ID, Fee: fee.ID, ScheduledNS: scheduled.UnixNano()}
		nextIndex++
		report.Samples = append(report.Samples, s)
		lanes[chosen] = l
		lanes[chosen].Busy = true
		inflight++
		jobs <- e8Job{s, l, fee, origin}
	}
	close(jobs)
	for inflight > 0 {
		consume(<-done)
	}
	workers.Wait()
	deadline := end.Add(o.Drain)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		retire()
		mu.Lock()
		remaining := len(pending)
		mu.Unlock()
		if remaining == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	observers.Wait()
	stopFollow()
	report.FinishedNS = time.Now().UnixNano()
	report.ProgressErrors = progressErrors
	report.Descriptor = protocol.DescriptorMetrics()
	if e = cfg.Write(filepath.Join(dir, "reports", "e8-report.json"), report); e != nil {
		return e
	}
	complete, expected := 0, 0
	for _, s := range report.Samples {
		if s.SentNS == 0 && s.Error == "not sent: window ended" {
			continue
		}
		expected++
		if s.PublicNS > 0 && s.MemberNS[0] > 0 && s.MemberNS[1] > 0 && s.MemberNS[2] > 0 && s.MemberNS[3] > 0 {
			complete++
		}
	}
	fmt.Printf("E8 dispatched=%d complete=%d slots=%d skipped=%d no_ready=%d pending_full=%d\n", len(report.Samples), complete, report.Scheduled, report.SkippedPacing, report.NoReady, report.PendingFull)
	if err != nil {
		return err
	}
	if complete != expected {
		return fmt.Errorf("incomplete cohort %d/%d", complete, expected)
	}
	return nil
}
