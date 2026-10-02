//go:build comet_v3

package redaction

import (
	cmtstore "github.com/cometbft/cometbft/store"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

// Pending entries may be removed after a representation command commits;
// the separate immutable index remains available for canonical reconstruction.
func decisionIndexKey(kind uint8, height int64, out protocol.OutputID) []byte {
	e := new(protocol.Encoder)
	e.U64(uint64(height))
	e.Fixed(out[:])
	return state.Key(kind, e.Data())
}
func DecisionPendingKey(height int64, out protocol.OutputID) []byte {
	return decisionIndexKey(116, height, out)
}
func DecisionIndexKey(height int64, out protocol.OutputID) []byte {
	return decisionIndexKey(117, height, out)
}

func ExecuteDecision(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, c protocol.CompensationDecision, height, now int64) (state.Transition, error) {
	if c.ID() == (protocol.Hash{}) || c.Height >= height {
		return state.Transition{}, protocol.ErrRule
	}
	if todo, found, err := state.Load[rules.DirectRepairTodo](v, rules.DirectRepairKey(c.Output)); err != nil {
		return state.Transition{}, err
	} else if found {
		if todo.Decision != c || todo.DecisionHeight <= 0 {
			return state.Transition{}, protocol.ErrAuth
		}
		raw, err := (protocol.CompensationResult{Decision: c.ID()}).MarshalBinary()
		return state.Transition{Data: raw}, err
	}
	// Reject mismatched public requests before loading/decoding a historical
	// block. The decision is permissionless, but its target is not caller-chosen.
	ob, found, err := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(c.Output))
	if err != nil {
		return state.Transition{}, err
	}
	if !found || ob.Status != rules.DirectOpen {
		return state.Transition{}, rules.ErrMissing
	}
	location, found, err := state.Load[Location](v, LocationKey(ob.Transaction))
	if err != nil {
		return state.Transition{}, err
	}
	cfg, known := p.Organizations[ob.Config]
	if !found || !known || cfg.Network != c.Network || location.Height != c.Height || location.Index != c.Transaction || ob.Input != c.Input {
		return state.Transition{}, protocol.ErrAuth
	}
	target, pay, err := CompensationTarget(v, bs, p, c.Output, now)
	if err != nil {
		return state.Transition{}, err
	}
	if c.Network != target.Network || c.Height != target.Height || c.Transaction != target.Transaction || c.Input != target.Input {
		return state.Transition{}, protocol.ErrAuth
	}
	// Replacement is fixed width. Check representability without computing a
	// collision or contacting an adapter before authorizing the debit.
	original, err := pay.MarshalBinary()
	if err != nil {
		return state.Transition{}, err
	}
	pay.Tx.Funding[c.Input].Kind = protocol.ReserveFunding
	pay.Tx.Funding[c.Input].Ref = protocol.ReserveDebitIdentity(c.Network, c.Output)
	replaced, err := pay.MarshalBinary()
	if err != nil || len(original) != len(replaced) {
		return state.Transition{}, protocol.ErrRule
	}
	tr, err := rules.EvaluateDirectCompensation(v, c.Output, p, now)
	if err != nil {
		return state.Transition{}, err
	}
	effect, err := protocol.DecodeExecution(tr.Data)
	if err != nil {
		return state.Transition{}, err
	}
	if !effect.Applied {
		return state.Transition{}, rules.ErrConflict
	}
	o := state.NewOverlay(v)
	o.Apply(tr.Changes)
	todo, found, err := state.Load[rules.DirectRepairTodo](o, rules.DirectRepairKey(c.Output))
	if err != nil {
		return state.Transition{}, err
	}
	if !found {
		return state.Transition{}, rules.ErrMissing
	}
	todo.Decision, todo.DecisionHeight = c, height
	if err = state.Put(o, rules.DirectRepairKey(c.Output), todo); err != nil {
		return state.Transition{}, err
	}
	for _, key := range [][]byte{DecisionPendingKey(c.Height, c.Output), DecisionIndexKey(c.Height, c.Output)} {
		if err = state.Put(o, key, todo.Obligation); err != nil {
			return state.Transition{}, err
		}
	}
	ob = todo.Obligation
	raw, err := (protocol.CompensationResult{Decision: c.ID(), Applied: true, Effects: []protocol.RepairEffect{{Output: c.Output, ParentFact: ob.Certificate, ConsumerFact: ob.Consumer, ConsumerTx: ob.Transaction, Input: ob.Input, Amount: ob.Amount, Debit: todo.Debit}}, FeeOutputs: effect.FeeOutputs}).MarshalBinary()
	if err != nil {
		return state.Transition{}, err
	}
	return state.Transition{Changes: o.Changes(), Data: raw}, nil
}
