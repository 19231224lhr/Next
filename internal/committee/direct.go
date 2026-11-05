package committee

import (
	"bytes"
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
	if protocol.IsPoolCertificate(raw) {
		if !e.cfg.EnableInternalPools {
			return state.Transition{}, protocol.ErrUnsupported
		}
		c, err := protocol.DecodePoolCertificate(raw)
		if err != nil {
			return state.Transition{}, err
		}
		return rules.EvaluatePool(v, c, e.cfg.Network, e.direct.Organizations)
	}
	if protocol.IsReserveIncrease(raw) {
		c, err := protocol.DecodeReserveIncrease(raw)
		if err != nil {
			return state.Transition{}, err
		}
		if c.Network != e.cfg.Network {
			return state.Transition{}, protocol.ErrAuth
		}
		if c.Key.Kind == protocol.ResourceFUEL {
			authorized := false
			if e.direct.C3 != nil {
				for _, i := range e.direct.C3.Issuers {
					if i.Config == c.Organization && i.Funding.Key == c.Key && i.Funding.Grant == c.Grant {
						authorized = true
					}
				}
			}
			if !authorized {
				return state.Transition{}, protocol.ErrAuth
			}
		}
		tr, err := rules.EvaluateReserveIncrease(v, c, e.protectedCAL)
		if err != nil || !e.direct.FinalC3() {
			return tr, err
		}
		// The command's Previous amount may already include a begin-block
		// epoch credit. Publish that authenticated cumulative credit on this
		// result so followers apply it before replaying the strict top-up.
		epoch, _, err := state.Load[rules.C3EpochState](v, rules.C3EpochKey())
		if err != nil {
			return state.Transition{}, err
		}
		tr.Data, err = (protocol.ExecutionResult{C3Credits: epoch.Credits}).MarshalBinary()
		return tr, err
	}
	if protocol.IsClockTick(raw, e.cfg.Network) {
		if e.direct.FinalC3() {
			epoch, _, err := state.Load[rules.C3EpochState](v, rules.C3EpochKey())
			if err != nil {
				return state.Transition{}, err
			}
			data, err := (protocol.ExecutionResult{C3Credits: epoch.Credits}).MarshalBinary()
			return state.Transition{Data: data}, err
		}
		return state.Transition{}, nil
	}
	if protocol.IsRepairInput(raw) || protocol.IsRepairBatch(raw) || protocol.IsCompensationDecision(raw) {
		if e.repairExecute == nil {
			return state.Transition{}, protocol.ErrUnsupported
		}
		return e.repairExecute(v, raw, b)
	}
	verified, err := e.verifyV3(raw)
	if err != nil {
		return state.Transition{}, err
	}
	p := *e.direct
	if p.FinalC3() {
		for _, validator := range p.C3.Committee {
			if bytes.Equal(validator.Address, b.ProposerAddress) {
				p.Includer = validator.ID
			}
		}
		if p.Includer == (protocol.Hash{}) {
			return state.Transition{}, protocol.ErrAuth
		}
	}
	tr, err := rules.EvaluateDirectPaymentAt(v, verified, p, b.Time.Unix(), b.Height)
	if err != nil || len(tr.Changes) == 0 {
		return tr, err
	}
	o := state.NewOverlay(v)
	o.Apply(tr.Changes)
	if err = state.Put(o, PaymentLocationKey(verified.TxID()), PaymentLocation{Height: b.Height, Index: b.Index}); err != nil {
		return state.Transition{}, err
	}
	tr.Changes = o.Changes()
	if p.FinalC3() && len(tr.Data) > 0 {
		result, err := protocol.DecodeExecution(tr.Data)
		if err != nil {
			return state.Transition{}, err
		}
		epoch, _, err := state.Load[rules.C3EpochState](o, rules.C3EpochKey())
		if err != nil {
			return state.Transition{}, err
		}
		result.C3Credits = epoch.Credits
		tr.Data, err = result.MarshalBinary()
		if err != nil {
			return state.Transition{}, err
		}
	}
	return tr, nil
}

func (e *Engine) BeginBlock(v state.ReadView, b BlockContext) (state.Transition, error) {
	if e.direct == nil {
		return state.Transition{}, nil
	}
	tr, err := rules.BeginC3Epoch(v, *e.direct, uint64(b.Height))
	if err != nil {
		return state.Transition{}, err
	}
	o := state.NewOverlay(v)
	o.Apply(tr.Changes)
	anchored, err := rules.AnchorDirectDeadlines(v, *e.direct, b.Height, b.Time.Unix())
	if err != nil {
		return state.Transition{}, err
	}
	o.Apply(anchored.Changes)
	return state.Transition{Changes: o.Changes()}, nil
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
