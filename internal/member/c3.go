package member

import (
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

// Replay fee facts even when this member did not sign the source. A later own
// approval must inherit the public cumulative state, never reconstruct a refund.
func (m *Member) c3Event(o *state.Overlay, s protocol.OutputSummary, stage rules.C3Stage, index uint32) error {
	if !s.C3.Enabled() {
		return nil
	}
	if err := rules.ValidateC3Summary(s, *m.direct); err != nil {
		return err
	}
	fact := s.Fact()
	key := rules.C3EscrowKey(fact)
	rec, found, err := state.Load[rules.C3Record](o, key)
	if err != nil {
		return err
	}
	if !found {
		rec.Escrow, err = rules.NewC3Escrow(s.Network, s.Config, fact, s.C3)
		if err != nil {
			return err
		}
		rec.Escrow, err = rec.Escrow.Apply(s.C3, rules.C3Attach, rules.C3WholeTransaction)
		if err != nil {
			return err
		}
	}
	rec.Escrow, err = rec.Escrow.Apply(s.C3, stage, index)
	if err != nil {
		return err
	}
	if stage == rules.C3Execute {
		rec.QuoteCollected = true
	}
	if err = state.Put(o, key, rec); err != nil {
		return err
	}
	a, found, err := state.Load[state.Approval](o, state.Key(state.KeyApproval, fact[:]))
	if err != nil || !found {
		return err
	}
	p, _, err := state.Load[LocalProgress](o, ProgressKey(fact))
	if err != nil {
		return err
	}
	return m.finishLocal(o, &a, &p)
}

func (m *Member) c3Compensate(o *state.Overlay, fact protocol.SpendFactID, id protocol.OutputID) error {
	rec, found, err := state.Load[rules.C3Record](o, rules.C3EscrowKey(fact))
	if err != nil || !found {
		return err
	}
	// Fact alone does not recover the transaction ID. The public source summary
	// is persisted on first missing-input observation, independently of approval.
	s, ok, err := state.Load[protocol.OutputSummary](o, state.Key(171, fact[:]))
	if err != nil {
		return err
	}
	if !ok {
		return rules.ErrMissing
	}
	if rec.Escrow.Commitment != s.C3 {
		return protocol.ErrAuth
	}
	for i := range s.Outputs {
		if s.OutputID(uint32(i)) == id {
			return m.c3Event(o, s, rules.C3Compensate, uint32(i))
		}
	}
	return protocol.ErrAuth
}
