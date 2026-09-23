package rules

import (
	"testing"
	"utxo/internal/state"
	"utxo/protocol"
)

func TestReserveIncreaseConservesFundsAndReplays(t *testing.T) {
	f := newDirectFixture(t)
	ref := f.grants[0]
	c := protocol.ReserveIncrease{Network: f.org.Network, Organization: f.org.Hash(), Key: ref.Key, Grant: ref.Grant, Previous: 10000000, Amount: 300}
	c.Sign(f.owner)
	source := AccountKey(protocol.ReserveFundingAccount(c.Network, c.Subject), protocol.AssetCAL)
	if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		err := state.Put(o, source, uint64(500))
		return o.Changes(), err
	}); err != nil {
		t.Fatal(err)
	}
	apply := func(c protocol.ReserveIncrease) error {
		return f.db.Update(func(v state.ReadView) ([]state.Change, error) {
			tr, e := EvaluateReserveIncrease(v, c)
			return tr.Changes, e
		})
	}
	if err := apply(c); err != nil {
		t.Fatal(err)
	}
	if err := apply(c); err != nil {
		t.Fatal("replay", err)
	}
	if got := loadDirect[uint64](t, f.db, source); got != 200 {
		t.Fatal("source", got)
	}
	if got := loadDirect[uint64](t, f.db, AccountKey(f.org.Org, protocol.AssetCAL)); got != 10000300 {
		t.Fatal("backing", got)
	}
	g := loadDirect[state.Grant](t, f.db, state.Key(state.KeyGrant, ref.Key.Encode()))
	if g.ID != ref.Grant || g.Amount != 10000300 {
		t.Fatal(g)
	}
	c.Previous = g.Amount
	c.Amount = 300
	c.Sign(f.owner)
	if err := apply(c); err == nil {
		t.Fatal("overdraw accepted")
	}
	c.Previous = 10000000
	c.Amount = 100
	c.Sign(f.owner)
	if err := apply(c); err == nil {
		t.Fatal("stale update accepted")
	}
	c.Previous = g.Amount
	c.Signature[0] ^= 1
	if err := apply(c); err == nil {
		t.Fatal("bad signature accepted")
	}
	if got := loadDirect[uint64](t, f.db, source); got != 200 {
		t.Fatal("failed command mutated funds")
	}
}
