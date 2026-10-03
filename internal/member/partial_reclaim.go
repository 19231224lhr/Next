package member

import (
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

// consumePublicInput is called only by the authenticated successful-block path.
// INSTALL may set Consumed, but cannot invalidate a local approval.
func (m *Member) consumePublicInput(o *state.Overlay, winner protocol.DirectSubmission, output protocol.OutputID, instance uint8, height int64) error {
	key := rules.DirectSpendKey(output, instance)
	s, _, err := state.Load[state.Spend](o, key)
	if err != nil {
		return err
	}
	fact := winner.Authorization.Fact
	if s.Consumed != (protocol.SpendFactID{}) && s.Consumed != fact {
		return rules.ErrAccounting
	}
	if s.Candidate != (protocol.SpendFactID{}) && s.Candidate != fact {
		if err := m.reclaimPartial(o, s.Candidate, winner, output, instance, height); err != nil {
			return err
		}
	}
	return state.Put(o, key, state.Spend{Consumed: fact})
}

func (m *Member) reclaimPartial(o *state.Overlay, fact protocol.SpendFactID, winner protocol.DirectSubmission, output protocol.OutputID, instance uint8, height int64) error {
	a, found, err := state.Load[state.Approval](o, state.Key(state.KeyApproval, fact[:]))
	if err != nil {
		return err
	}
	if !found || a.Fact != fact || a.Direct == nil {
		return rules.ErrAccounting
	}
	if a.Direct.Body.Config != m.cfg.Organization.Hash() {
		return rules.ErrAccounting
	}
	if a.Direct.ID() == winner.Tx.ID() {
		return rules.ErrAccounting
	}
	shared := false
	for i, in := range a.Direct.Body.Inputs {
		if in.Output == output && i < len(a.Direct.Claims) && a.Direct.Claims[i].Instance == instance {
			shared = true
		}
	}
	for _, in := range a.Direct.Body.Fee.Inputs {
		if in.Output == output && instance == 0 {
			shared = true
		}
	}
	if !shared {
		return rules.ErrAccounting
	}
	p, _, err := state.Load[LocalProgress](o, ProgressKey(fact))
	if err != nil {
		return err
	}
	// Overlay visibility makes multiple conflicting inputs/transactions idempotent.
	if p.Invalidated {
		return nil
	}
	if p.Settled || p.Pending != 0 || p.Paid != 0 || p.Recovered != 0 || p.Fee != (rules.Escrow{}) {
		return rules.ErrAccounting
	}
	for _, prefix := range []byte{state.KeyObserved, state.KeyInstall, state.KeyOutbox} {
		if _, err := o.Get(state.Key(prefix, fact[:])); err == nil {
			return rules.ErrAccounting
		} else if err != state.ErrNotFound {
			return err
		}
	}
	p.Applied, err = localApplied(a, p)
	if err != nil {
		return err
	}
	for i, d := range a.Debits {
		delta := d.Cap - p.Applied[i]
		if delta == 0 {
			continue
		}
		key := state.SliceKey(d.Key, d.Worker)
		s, found, err := state.Load[state.Slice](o, key)
		if err != nil {
			return err
		}
		if !found {
			return rules.ErrMissing
		}
		s.Reserved, err = protocol.Sub(s.Reserved, delta)
		if err != nil {
			return err
		}
		s.Available, err = protocol.Add(s.Available, delta)
		if err != nil {
			return err
		}
		if err = state.Put(o, key, s); err != nil {
			return err
		}
		p.Applied[i] = d.Cap
	}
	p.Invalidated = true
	p.SupersededBy = winner.Authorization.Fact
	p.InvalidatedHeight = height
	return state.Put(o, ProgressKey(fact), p)
}
