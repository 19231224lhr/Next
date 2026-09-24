package main

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/member"
	"utxo/internal/state"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

func (w *e7Wallet) follow(ctx context.Context, cancel context.CancelFunc, owners map[protocol.PublicKey]bool) (func(), error) {
	trust, e := w.network.Trust()
	if e != nil {
		return nil, e
	}
	prepare := wallet.PrepareOwnersBlock(w.network.Genesis.Network, owners)
	rows := map[protocol.TxID]e7Location{}
	wrapped := func(b finality.VerifiedBlock) (blockfollow.Apply, error) {
		apply, e := prepare(b)
		if e != nil {
			return nil, e
		}
		clear(rows)
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
			rows[p.Tx.ID()] = e7Location{b.Height(), len(r.MissingInputs)}
		}
		return apply, nil
	}
	stop := blockfollow.Start(ctx, cancel, w.db, transport.NewCommitteeClient(w.network.CommitteeURLs[0]), trust, wrapped, func(_ int64) {
		w.mu.Lock()
		defer w.mu.Unlock()
		for tx, row := range rows {
			w.locations[tx] = row
		}
	})
	var wg sync.WaitGroup
	for replica := 0; replica < 4; replica++ {
		wg.Add(1)
		go func(replica int) {
			defer wg.Done()
			client := transport.NewHTTPClient(3 * time.Second)
			for ctx.Err() == nil {
				w.mu.Lock()
				var todo []*e7Sample
				for _, s := range w.pending {
					if s.ReadyNS > 0 && s.MemberNS[replica] == 0 {
						todo = append(todo, s)
					}
				}
				w.mu.Unlock()
				for start := 0; start < len(todo); start += member.MaxProgressBatch {
					batch := todo[start:min(start+member.MaxProgressBatch, len(todo))]
					facts := make([]protocol.SpendFactID, len(batch))
					for i, s := range batch {
						facts[i] = s.Fact
					}
					statuses, e := loadProgressBatch(ctx, client, w.network.Members[w.network.Organizations[w.config.Site].Org][replica], facts)
					w.mu.Lock()
					if e != nil {
						if w.report != nil {
							w.report.ProgressErrors++
						}
					} else {
						for i, s := range batch {
							if statuses[i].Observed && (!statuses[i].Signed || statuses[i].Closed) {
								s.observeMember(replica, time.Now())
							}
						}
					}
					w.mu.Unlock()
				}
				if budgetPause(ctx, 50*time.Millisecond) != nil {
					return
				}
			}
		}(replica)
	}
	return func() { stop(); wg.Wait() }, nil
}

func (w *e7Wallet) retire() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, s := range w.pending {
		if loc, ok := w.locations[s.Tx]; ok && s.PublicNS == 0 {
			s.observePublic(time.Now())
			s.Height = loc.Height
			s.Missing = loc.Missing
		}
		if s.PublicNS > 0 && s.MemberNS[0] > 0 && s.MemberNS[1] > 0 && s.MemberNS[2] > 0 && s.MemberNS[3] > 0 {
			delete(w.pending, i)
		}
	}
}

func (w *e7Wallet) run(ctx context.Context, o e7RunOptions) {
	w.mu.Lock()
	end := w.end
	report := w.report
	q := w.ready
	w.mu.Unlock()
	start := end.Add(-o.Duration)
	report.StartedNS = start.UnixNano()
	report.EndSendNS = end.UnixNano()
	defer func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		report.FinishedNS = time.Now().UnixNano()
		if e := cfg.Write(filepath.Join(w.config.Dir, "reports", fmt.Sprintf("e7-%s-%d.json", o.Phase, w.config.Site)), report); e != nil {
			report.Error = e.Error()
		}
		w.active = false
	}()
	if budgetPause(ctx, time.Until(start)) != nil {
		return
	}
	done := make(chan *e7Sample, o.Fast)
	inflight := 0
	consume := func(s *e7Sample) {
		inflight--
		w.mu.Lock()
		report.Samples = append(report.Samples, s)
		if s.SentNS > 0 {
			w.pending[s.Index] = s
		}
		w.mu.Unlock()
		if !o.Chain && s.ReadyNS > 0 && time.Now().Before(end) {
			q.add(e7Task{Lane: s.Lane, Generation: s.Generation + 1, Hop: 1, Owner: s.Lane % 64, Parent: -1, receivedAt: time.Now()})
		}
	}
	due := start
	for time.Now().Before(end) && ctx.Err() == nil {
		for inflight > 0 {
			select {
			case s := <-done:
				consume(s)
			default:
				goto drained
			}
		}
	drained:
		w.retire()
		now := time.Now()
		if now.Before(due) {
			select {
			case s := <-done:
				consume(s)
			case <-time.After(min(time.Until(due), 10*time.Millisecond)):
			case <-ctx.Done():
			}
			continue
		}
		scheduled := due
		var missed int
		due, missed = e8NextSlot(due, now, time.Second/time.Duration(o.Rate/2))
		report.Scheduled += 1 + missed
		report.SkippedPacing += missed
		if inflight >= o.Fast {
			report.WorkerFull++
			continue
		}
		w.mu.Lock()
		busy := len(w.pending)+inflight >= o.Pending
		w.mu.Unlock()
		if busy {
			report.PendingFull++
			continue
		}
		task, ok := q.take()
		if !ok {
			report.NoReady++
			continue
		}
		owner := task.Owner
		if owner%2 != w.config.Site {
			report.Error = "wrong-site continuation"
			break
		}
		if w.fi[owner] >= len(w.fuel[owner]) {
			report.Error = "FUEL pool exhausted"
			break
		}
		fee := w.fuel[owner][w.fi[owner]]
		w.fi[owner]++
		var origin *state.OriginOutput
		if task.Hop == 1 {
			if w.ci[owner] >= len(w.cal[owner]) {
				report.Error = "CAL pool exhausted"
				break
			}
			origin = &w.cal[owner][w.ci[owner]]
			w.ci[owner]++
			task.Input = origin.ID
		}
		index := w.next*2 + w.config.Site
		w.next++
		s := &e7Sample{e8Sample: e8Sample{Index: index, Lane: task.Lane, Hop: task.Hop, Sender: owner, Receiver: e7Recipient(owner, 64, o.Seed, o.Cross), Parent: task.Parent, Input: task.Input, Fee: fee.ID, ScheduledNS: scheduled.UnixNano()}, Phase: o.Phase, Generation: task.Generation, ReadyQueueNS: int64(now.Sub(task.receivedAt))}
		inflight++
		go func() { done <- w.pay(ctx, o, task, s, origin, fee, scheduled, start, end) }()
	}
	for inflight > 0 {
		consume(<-done)
	}
	for time.Now().Before(end.Add(o.Drain)) && ctx.Err() == nil {
		w.retire()
		w.mu.Lock()
		left := len(w.pending)
		w.mu.Unlock()
		if left == 0 {
			break
		}
		budgetPause(ctx, 50*time.Millisecond)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 && report.Error == "" {
		report.Error = fmt.Sprintf("%d payments unfinished", len(w.pending))
	}
}

func (w *e7Wallet) pay(ctx context.Context, o e7RunOptions, task e7Task, s *e7Sample, origin *state.OriginOutput, fee state.OriginOutput, scheduled, start, end time.Time) *e7Sample {
	build := time.Now()
	s.BuildNS = build.UnixNano()
	var coin wallet.DirectCoin
	var e error
	if origin != nil {
		coin = wallet.DirectCoin{Output: origin.Output, Final: origin.Fact}
	} else {
		e = w.db.View(func(v state.ReadView) error {
			c, ok, e := state.Load[wallet.DirectCoin](v, wallet.DirectCoinKey(task.Input, 0))
			coin = c
			if !ok && e == nil {
				return state.ErrNotFound
			}
			return e
		})
	}
	var req protocol.DirectRequest
	var raw []byte
	if e == nil {
		var in protocol.Input
		var proofs []protocol.InputCertificate
		in, proofs, e = chainInput(task.Input, coin)
		s.CertificateInput = in.Kind == protocol.CertificateInput
		if e == nil {
			req, e = budgetRequest(w.network, w.policy, w.network.Organizations[w.config.Site], w.keys[s.Sender], in, coin.Output, w.descriptors[s.Receiver], proofs, fee)
		}
	}
	if e == nil {
		e = w.wallets[s.Sender].SaveDirectRequest(req)
	}
	if e == nil {
		raw, e = req.MarshalBinary()
	}
	if e != nil {
		s.Error = e.Error()
		return s
	}
	if !time.Now().Before(end) {
		s.Error = "not sent: window ended"
		return s
	}
	s.Tx = req.Tx.ID()
	s.Output = protocol.OutputIdentity(w.network.Genesis.Network, s.Tx, 0)
	s.RequestBytes = len(raw)
	s.sentAt = time.Now()
	s.SentNS = s.sentAt.UnixNano()
	s.SentElapsedNS = int64(s.sentAt.Sub(start))
	s.Monotonic.BuildNS = int64(s.sentAt.Sub(build))
	s.Monotonic.ScheduleLagNS = int64(s.sentAt.Sub(scheduled))
	if o.Chain && s.Hop == 1 {
		w.mu.Lock()
		w.roots[[2]int{s.Lane, s.Generation}] = s.sentAt
		w.mu.Unlock()
	}
	client := w.client()
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	encoded, _, attempts, e := submitChain(call, client, w.config.Gateway+"/v3/transactions", raw, false)
	s.Attempts = attempts
	got := time.Now()
	s.ReceivedNS = got.UnixNano()
	s.Monotonic.ResponseNS = int64(got.Sub(s.sentAt))
	if e == nil {
		var cert protocol.OutputCertificate
		cert, e = protocol.DecodeOutputCertificate(encoded)
		if e == nil {
			s.Fact = cert.QC.Fact
			s.CertificateBytes = len(encoded)
			d := e7Delivery{Phase: o.Phase, Index: s.Index, Lane: s.Lane, Generation: s.Generation, Hop: s.Hop, Sender: s.Sender, Receiver: s.Receiver, Parent: s.Parent, Output: req.Tx.Body.Outputs[0], Certificate: encoded}
			delivered := time.Now()
			var a e7Ack
			for retry := 0; retry < 3; retry++ {
				a, e = e7SendReceipt(call, client, w.config.Receivers[s.Receiver%2], d)
				if e == nil || call.Err() != nil {
					break
				}
				budgetPause(call, 20*time.Millisecond)
			}
			s.DeliveryNS = int64(time.Since(delivered))
			s.ReceiveNS = a.ReceiveNS
			if e == nil && a.Fact != s.Fact {
				e = fmt.Errorf("receipt fact mismatch")
			}
		}
	}
	if e != nil {
		s.Error = e.Error()
		return s
	}
	ready := time.Now()
	s.ReadyNS = ready.UnixNano()
	s.Monotonic.FastNS = int64(ready.Sub(s.sentAt))
	s.Monotonic.WalletNS = int64(ready.Sub(got))
	return s
}

var e7HTTP = transport.NewHTTPClient(3 * time.Second)

func (w *e7Wallet) client() *http.Client { return e7HTTP }
