package rules

import (
	"testing"
	"utxo/internal/state"
	"utxo/protocol"
)

func TestReserveMemberKeepsDebitsAndCumulativeRounding(t *testing.T) {
	f := newDirectFixture(t)
	ref := f.grants[0]
	c := protocol.ReserveIncrease{Network: f.org.Network, Organization: f.org.Hash(), Key: ref.Key, Grant: ref.Grant, Previous: 10000000, Amount: 1}
	c.Sign(f.owner)
	err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		for w := uint32(0); w < 3; w++ {
			if err := state.Put(o, state.SliceKey(ref.Key, w), state.Slice{Available: 0, Reserved: 50, Generation: 9}); err != nil {
				return nil, err
			}
		}
		return o.Changes(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	apply := func(c protocol.ReserveIncrease) error {
		return f.db.Update(func(v state.ReadView) ([]state.Change, error) {
			o := state.NewOverlay(v)
			err := ApplyReserveIncrease(o, c, f.org.Hash(), 3)
			return o.Changes(), err
		})
	}
	if err = apply(c); err != nil {
		t.Fatal(err)
	}
	if err = apply(c); err != nil {
		t.Fatal(err)
	}
	c.Previous++
	c.Amount = 2
	c.Sign(f.owner)
	if err = apply(c); err != nil {
		t.Fatal(err)
	}
	var free uint64
	for w := uint32(0); w < 3; w++ {
		s := loadDirect[state.Slice](t, f.db, state.SliceKey(ref.Key, w))
		free += s.Available
		if s.Reserved != 50 || s.Generation != 9 {
			t.Fatal("old debit changed", s)
		}
	}
	if free != 2 {
		t.Fatal("cumulative share rounding", free)
	}
	g := loadDirect[state.Grant](t, f.db, state.Key(state.KeyGrant, ref.Key.Encode()))
	if g.ID != ref.Grant || g.Amount != 10000003 {
		t.Fatal(g)
	}
}
