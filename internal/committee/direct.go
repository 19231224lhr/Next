package committee

import (
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
	p, err := protocol.DecodeDirectSubmission(raw)
	if err != nil {
		return rules.VerifiedDirectPayment{}, err
	}
	if p.Tx.Body.Network != e.cfg.Network {
		return rules.VerifiedDirectPayment{}, protocol.ErrAuth
	}
	return rules.VerifyDirectSubmission(p, *e.direct)
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
	p, err := protocol.DecodeDirectSubmission(raw)
	if err != nil {
		return state.Transition{}, err
	}
	o := state.NewOverlay(v)
	o.Apply(tr.Changes)
	if err = state.Put(o, PaymentLocationKey(p.Tx.ID()), PaymentLocation{Height: b.Height, Index: b.Index}); err != nil {
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
