package committee

import (
	"time"
	"utxo/internal/requesttrace"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

type PaymentLocation struct {
	Height int64
	Index  uint32
}

func PaymentLocationKey(tx protocol.TxID) []byte { return state.Key(107, tx[:]) }

func (e *Engine) verifyV3(raw []byte) (rules.VerifiedDirectPayment, error) {
	if e.direct == nil {
		return rules.VerifiedDirectPayment{}, protocol.ErrRule
	}
	// TxID deliberately survives funding repair and excludes OwnerAuth. Cache
	// only the exact public bytes, within this Engine's fixed policy snapshot.
	trace := requesttrace.Consensus
	var started time.Time
	if trace != nil {
		started = time.Now()
	}
	key := protocol.Digest("VERIFIED_DIRECT_SUBMISSION_BYTES", raw)
	verified, hit := e.directCache.get(key)
	if trace != nil {
		defer func() {
			hits, misses, evictions, oversized, entries, bytes := e.directCache.stats()
			trace.Mark("direct_verify", "key", key.String(), "cached", hit, "duration_ns", time.Since(started).Nanoseconds(), "hits", hits, "misses", misses, "evictions", evictions, "oversized", oversized, "entries", entries, "bytes", bytes)
		}()
	}
	if hit {
		return verified, nil
	}
	p, err := protocol.DecodeDirectSubmission(raw)
	if err != nil {
		return rules.VerifiedDirectPayment{}, err
	}
	if p.Tx.Body.Network != e.cfg.Network {
		return rules.VerifiedDirectPayment{}, protocol.ErrAuth
	}
	verified, err = rules.VerifyDirectSubmission(p, *e.direct)
	if err == nil {
		e.directCache.put(key, verified, len(raw))
	}
	return verified, err
}

func (e *Engine) ExecuteAt(v state.ReadView, raw []byte, b BlockContext) (state.Transition, error) {
	if e.direct == nil {
		return e.Execute(v, raw)
	}
	if b.Height <= 0 || b.Time.IsZero() {
		return state.Transition{}, protocol.ErrRule
	}
	if protocol.IsClockTick(raw, e.cfg.Network) {
		return state.Transition{}, nil
	}
	if protocol.IsRepairInput(raw) {
		if e.repairExecute == nil {
			return state.Transition{}, protocol.ErrUnsupported
		}
		return e.repairExecute(v, raw, b)
	}
	verified, err := e.verifyV3(raw)
	if err != nil {
		return state.Transition{}, err
	}
	tr, err := rules.EvaluateDirectPaymentAt(v, verified, *e.direct, b.Time.Unix(), b.Height)
	if err != nil || len(tr.Changes) == 0 {
		return tr, err
	}
	o := state.NewOverlay(v)
	o.Apply(tr.Changes)
	if err = state.Put(o, PaymentLocationKey(verified.TxID()), PaymentLocation{Height: b.Height, Index: b.Index}); err != nil {
		return state.Transition{}, err
	}
	tr.Changes = o.Changes()
	return tr, nil
}

func (e *Engine) BeginBlock(v state.ReadView, b BlockContext) (state.Transition, error) {
	if e.direct == nil {
		return state.Transition{}, nil
	}
	return rules.AnchorDirectDeadlines(v, *e.direct, b.Height, b.Time.Unix())
}

func (e *Engine) DirectPayment(id protocol.SpendFactID) (p rules.DirectPaymentState, err error) {
	if e.direct == nil {
		return p, protocol.ErrUnsupported
	}
	err = e.db.View(func(v state.ReadView) error {
		var found bool
		var err error
		p, found, err = state.Load[rules.DirectPaymentState](v, state.Key(103, id[:]))
		if err == nil && !found {
			return state.ErrNotFound
		}
		return err
	})
	return p, err
}
