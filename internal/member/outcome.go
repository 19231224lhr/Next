package member

import (
	"utxo/internal/state"
	"utxo/protocol"
)

type Outcome struct {
	Approved, Installed, PublicObserved, Custody                   bool
	FuelResidual, PolicyResidual, ExecutionResidual, BytesResidual uint64
}

func (m *Member) Outcome(id protocol.SpendFactID) (out Outcome, err error) {
	err = m.db.View(func(v state.ReadView) error {
		approval, found, e := state.Load[state.Approval](v, state.Key(state.KeyApproval, id[:]))
		if e != nil {
			return e
		}
		out.Approved = found
		applied, e := AppliedDebits(v, approval)
		if e != nil {
			return e
		}
		for i, d := range approval.Debits {
			residual, e := protocol.Sub(d.Cap, applied[i])
			if e != nil {
				return e
			}
			switch d.Key.Kind {
			case protocol.ResourceFUEL:
				out.FuelResidual = residual
			case protocol.ResourcePolicy:
				out.PolicyResidual = residual
			case protocol.ResourceExecution:
				out.ExecutionResidual = residual
			case protocol.ResourceBytes:
				out.BytesResidual = residual
			}
		}
		if _, e = v.Get(state.Key(state.KeyInstall, id[:])); e == nil {
			out.Installed = true
		} else if e != state.ErrNotFound {
			return e
		}
		out.PublicObserved, _, e = state.Load[bool](v, state.Key(state.KeyObserved, id[:]))
		if e != nil {
			return e
		}
		_, out.Custody, e = state.Load[state.Custody](v, state.Key(state.KeyCustody, id[:]))
		return e
	})
	return
}
