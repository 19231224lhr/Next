package rules

import (
	"utxo/internal/state"
	"utxo/protocol"
)

// protectedCAL contains the full account keys of all configured CAL backing.
// It is derived from the Engine's frozen configuration, never from the command.
func EvaluateReserveIncrease(v state.ReadView, c protocol.ReserveIncrease, protectedCAL map[string]struct{}) (state.Transition, error) {
	var tr state.Transition
	if err := c.Verify(); err != nil {
		return tr, err
	}
	id := c.ID()
	seen := state.Key(160, id[:])
	if _, found, err := state.Load[bool](v, seen); err != nil || found {
		return tr, err
	}
	key := state.Key(state.KeyGrant, c.Key.Encode())
	g, found, err := state.Load[state.Grant](v, key)
	if err != nil {
		return tr, err
	}
	if !found || g.ID != c.Grant || g.Organization != c.Organization || g.Subject != c.Subject {
		return tr, protocol.ErrAuth
	}
	if g.Amount != c.Previous {
		return tr, protocol.ErrRule
	}
	source := AccountKey(protocol.ReserveFundingAccount(c.Network, c.Subject), protocol.AssetCAL)
	destination := AccountKey(c.Key.Account, protocol.AssetCAL)
	_, sourceProtected := protectedCAL[string(source)]
	_, destinationProtected := protectedCAL[string(destination)]
	if string(source) == string(destination) || sourceProtected || !destinationProtected {
		return tr, protocol.ErrRule
	}
	balance, _, err := state.Load[uint64](v, source)
	if err != nil {
		return tr, err
	}
	remaining, err := protocol.Sub(balance, c.Amount)
	if err != nil {
		return tr, ErrLimited
	}
	backing, _, err := state.Load[uint64](v, destination)
	if err != nil {
		return tr, err
	}
	backing, err = protocol.Add(backing, c.Amount)
	if err != nil {
		return tr, err
	}
	g.Amount += c.Amount // checked by Verify
	o := state.NewOverlay(v)
	for _, p := range []struct {
		k []byte
		v any
	}{{source, remaining}, {destination, backing}, {key, g}, {seen, true}} {
		if err = state.Put(o, p.k, p.v); err != nil {
			return tr, err
		}
	}
	tr.Changes = o.Changes()
	return tr, nil
}

// ApplyReserveIncrease is called only for a successful verified public block.
// Cumulative totals make repeated block delivery harmless; reserved debits remain.
func ApplyReserveIncrease(o *state.Overlay, c protocol.ReserveIncrease, org protocol.Hash, workers uint32) error {
	if c.Organization != org {
		return nil
	}
	if workers == 0 {
		return protocol.ErrRule
	}
	key := state.Key(state.KeyGrant, c.Key.Encode())
	g, found, err := state.Load[state.Grant](o, key)
	if err != nil {
		return err
	}
	if !found || g.ID != c.Grant || g.Subject != c.Subject {
		return protocol.ErrAuth
	}
	target, err := protocol.Add(c.Previous, c.Amount)
	if err != nil {
		return err
	}
	if g.Amount >= target {
		return nil
	}
	if g.Amount != c.Previous {
		return protocol.ErrRule
	}
	oldShare, err := protocol.GrantShare(g.Amount)
	if err != nil {
		return err
	}
	newShare, err := protocol.GrantShare(target)
	if err != nil {
		return err
	}
	delta := newShare - oldShare
	for w := uint32(0); w < workers; w++ {
		k := state.SliceKey(g.Key, w)
		s, ok, e := state.Load[state.Slice](o, k)
		if e != nil {
			return e
		}
		if !ok {
			return ErrMissing
		}
		amount := delta / uint64(workers)
		if uint64(w) < delta%uint64(workers) {
			amount++
		}
		s.Available, e = protocol.Add(s.Available, amount)
		if e != nil {
			return e
		}
		if e = state.Put(o, k, s); e != nil {
			return e
		}
	}
	g.Amount = target
	return state.Put(o, key, g)
}
