//go:build comet_v3

package main

import (
	"context"
	"encoding/json"
	cmtstore "github.com/cometbft/cometbft/store"
	"log/slog"
	"time"
	"utxo/crypto/chameleon"
	"utxo/internal/redaction"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type repairPending struct {
	command       protocol.RepairBatch
	raw           []byte
	expires, next time.Time
}

func (p repairPending) due(now time.Time) bool     { return !now.Before(p.next) }
func (p repairPending) expired(now time.Time) bool { return !now.Before(p.expires) }
func (p *repairPending) attempted(now time.Time)   { p.next = now.Add(2 * time.Second) }

// FIFO eviction only loses optional cryptographic work, never eligibility or a
// pending obligation. All maps here are local, bounded scheduling hints.
type repairOpeningCache struct {
	values map[protocol.Hash]chameleon.Opening
	keys   []protocol.Hash
	next   int
}

func newRepairOpeningCache(size int) *repairOpeningCache {
	return &repairOpeningCache{values: make(map[protocol.Hash]chameleon.Opening), keys: make([]protocol.Hash, size)}
}
func (c *repairOpeningCache) put(key protocol.Hash, value chameleon.Opening) {
	if _, ok := c.values[key]; !ok {
		delete(c.values, c.keys[c.next])
		c.keys[c.next] = key
		c.next = (c.next + 1) % len(c.keys)
	}
	c.values[key] = value
}

type repairWorker struct {
	db      store.Store
	blocks  *cmtstore.BlockStore
	policy  rules.DirectPolicy
	index   int
	head    func() (int64, int64)
	collect func(context.Context, string, any, func([][]chameleon.Contribution) error) error
	submit  func(context.Context, []byte) (uint32, error)
	network protocol.Hash
	pending map[int64]*repairPending
	cache   *repairOpeningCache
	cursor  []byte
	// A fixed ring remembers failed outputs and singleton fallback independently
	// of candidate leases. Eviction can cause extra work, not lost obligations.
	failures    map[protocol.OutputID]time.Time
	failureKeys [256]protocol.OutputID
	failureNext int
	single      map[int64]time.Time
}

func (w *repairWorker) failed(output protocol.OutputID, now time.Time) {
	if _, ok := w.failures[output]; !ok {
		delete(w.failures, w.failureKeys[w.failureNext])
		w.failureKeys[w.failureNext] = output
		w.failureNext = (w.failureNext + 1) % len(w.failureKeys)
	}
	w.failures[output] = now.Add(5 * time.Second)
}
func (w *repairWorker) fallback(height int64, now time.Time) {
	if len(w.single) >= 256 { // keep the longest remaining hints, with a fixed cap
		var oldest int64
		var until time.Time
		for h, t := range w.single {
			if until.IsZero() || t.Before(until) {
				oldest, until = h, t
			}
		}
		delete(w.single, oldest)
	}
	w.single[height] = now.Add(30 * time.Second)
}

// One worker owns candidates and cache. Bounded synchronous RPCs deliberately
// avoid detached callbacks and generations; materialization runs separately.
func (w *repairWorker) run(ctx context.Context) {
	w.pending = make(map[int64]*repairPending)
	w.cache = newRepairOpeningCache(256)
	w.failures = make(map[protocol.OutputID]time.Time)
	w.single = make(map[int64]time.Time)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.step(ctx)
		}
	}
}
func (w *repairWorker) step(ctx context.Context) {
	now := time.Now()
	for h, t := range w.single {
		if !now.Before(t) {
			delete(w.single, h)
		}
	}
	for h, p := range w.pending {
		valid := false
		err := w.db.View(func(v state.ReadView) error {
			_, done, e := state.Load[redaction.BatchTask](v, redaction.BatchTaskKey(p.command.ID()))
			if e != nil || done {
				return e
			}
			rev, _, e := state.Load[redaction.Revision](v, redaction.RevisionKey(h))
			if e != nil || rev.Number != p.command.Base {
				return e
			}
			for _, x := range p.command.Items {
				ob, found, e := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(x.Output))
				if e != nil || !found || ob.Status != rules.DirectOpen {
					return e
				}
			}
			valid = true
			return nil
		})
		if err != nil {
			continue
		}
		if !valid {
			delete(w.pending, h)
			continue
		}
		if p.expired(now) {
			w.fallback(h, now)
			delete(w.pending, h)
			continue
		}
		if p.due(now) {
			w.send(ctx, p)
		}
	}
	if ctx.Err() != nil || len(w.pending) >= 16 {
		return
	}
	entries, err := store.Scan(w.db, state.Key(112), w.cursor, 64)
	if err != nil {
		return
	}
	if len(entries) == 0 {
		w.cursor = nil
		return
	}
	groups := make(map[int64][]protocol.OutputID)
	var order []int64
	selected := 0
	height, publicNow := w.head()
	for _, entry := range entries {
		w.cursor = entry.Key // advance across busy, invalid and cooling entries too
		var ob rules.DirectObligation
		if json.Unmarshal(entry.Value, &ob) != nil {
			continue
		}
		if now.Unix() < ob.Deadline+int64(w.index) {
			w.cursor = nil
			break
		}
		if publicNow < ob.Deadline {
			_, _ = w.submit(ctx, protocol.ClockTick(w.network, height+1))
			w.cursor = nil
			break
		}
		if until := w.failures[ob.Output]; now.Before(until) {
			continue
		}
		var loc redaction.Location
		err = w.db.View(func(v state.ReadView) error {
			var found bool
			var e error
			loc, found, e = state.Load[redaction.Location](v, redaction.LocationKey(ob.Transaction))
			if e == nil && !found {
				return rules.ErrMissing
			}
			return e
		})
		if err != nil {
			continue
		}
		if _, busy := w.pending[loc.Height]; busy {
			continue
		}
		if _, exists := groups[loc.Height]; !exists {
			if len(w.pending)+len(groups) >= 16 {
				break
			}
			order = append(order, loc.Height)
		}
		groups[loc.Height] = append(groups[loc.Height], ob.Output)
		selected++
		// Stop at a selected singleton instead of skipping other same-block items.
		if now.Before(w.single[loc.Height]) || selected == 8 {
			break
		}
	}
	for _, h := range order {
		if ctx.Err() != nil {
			return
		}
		command, err := w.build(ctx, groups[h], publicNow)
		if err != nil {
			w.fallback(h, time.Now())
			for _, out := range groups[h] {
				w.failed(out, time.Now())
			}
			continue
		}
		raw, err := command.MarshalBinary()
		if err != nil {
			continue
		}
		p := &repairPending{command: command, raw: raw, expires: time.Now().Add(5 * time.Second)}
		w.pending[h] = p
		w.send(ctx, p)
	}
}
func (w *repairWorker) send(ctx context.Context, p *repairPending) {
	code, err := w.submit(ctx, p.raw)
	p.attempted(time.Now())
	if err == nil && code != 0 {
		w.fallback(p.command.Height, time.Now())
		delete(w.pending, p.command.Height)
	}
	slog.Debug("repair batch submit", "height", p.command.Height, "items", len(p.command.Items), "bytes", len(p.raw), "code", code, "error", err)
}
func (w *repairWorker) build(ctx context.Context, outputs []protocol.OutputID, now int64) (protocol.RepairBatch, error) {
	var selected []protocol.OutputID
	if err := w.db.View(func(v state.ReadView) error {
		var err error
		selected, err = redaction.SelectRepairInputs(v, w.blocks, w.policy, outputs, now)
		return err
	}); err != nil {
		return protocol.RepairBatch{}, err
	}
	accepted := make(map[protocol.OutputID]bool, len(selected))
	for _, output := range selected {
		accepted[output] = true
	}
	for _, output := range outputs {
		if !accepted[output] {
			w.failed(output, time.Now())
		}
	}
	if len(selected) == 0 {
		return protocol.RepairBatch{}, rules.ErrLimited
	}
	var singles []protocol.RepairInput
	for _, output := range selected {
		var command protocol.RepairInput
		var pay protocol.DirectSubmission
		err := w.db.View(func(v state.ReadView) error {
			var e error
			command, pay, e = redaction.InputTarget(v, w.blocks, w.policy, output, now)
			return e
		})
		if err != nil {
			continue
		} // parent completion or stale queue entry
		i := int(command.Input)
		old := pay.Tx.Funding[i]
		next := protocol.Funding{Kind: protocol.ReserveFunding, Ref: protocol.ReserveDebitIdentity(command.Network, output)}
		contextBytes := pay.Tx.FundingContext(i, w.policy.Key.KeyID())
		commitment := pay.Tx.Commitments[i]
		key := protocol.Digest("REPAIR_OPENING_CACHE_V1", contextBytes, old.ReferenceBytes(), next.ReferenceBytes(), commitment[:], old.Opening[:])
		opening, hit := w.cache.values[key]
		if hit && w.policy.Key.Verify(contextBytes, next.ReferenceBytes(), commitment, opening) {
			next.Opening = opening
			pay.Tx.Funding = append([]protocol.Funding(nil), pay.Tx.Funding...)
			pay.Tx.Funding[i] = next
			command.TransactionBytes, err = pay.MarshalBinary()
		} else {
			err = w.collect(ctx, "/v3/repair/input-share", command, func(votes [][]chameleon.Contribution) error {
				var shares []chameleon.Contribution
				for _, row := range votes {
					if len(row) == 1 {
						shares = append(shares, row[0])
					}
				}
				var e error
				command, e = redaction.ReplaceInput(w.policy, command, pay, shares)
				return e
			})
			if err == nil {
				var adapted protocol.DirectSubmission
				adapted, err = protocol.DecodeDirectSubmission(command.TransactionBytes)
				if err == nil {
					w.cache.put(key, adapted.Tx.Funding[i].Opening)
				}
			}
		}
		if err != nil {
			w.failed(output, time.Now())
			continue
		}
		singles = append(singles, command)
	}
	var batch protocol.RepairBatch
	err := w.db.View(func(v state.ReadView) error {
		var e error
		batch, e = redaction.BuildBatch(v, w.blocks, w.policy, singles, now)
		return e
	})
	if err != nil {
		return batch, err
	}
	err = w.collect(ctx, "/v3/repair/batch-part-shares", batch, func(votes [][]chameleon.Contribution) error {
		return w.db.View(func(v state.ReadView) error {
			var e error
			batch, e = redaction.CompleteBatchParts(v, w.blocks, w.policy, batch, now, votes)
			return e
		})
	})
	return batch, err
}
