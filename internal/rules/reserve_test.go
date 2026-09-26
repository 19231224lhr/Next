package rules

import (
	"fmt"
	"testing"
	"utxo/internal/state"
	"utxo/protocol"
)

func TestReserveIncreaseRejectsProtectedSource(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(fmt.Sprintf("same=%t", same), func(t *testing.T) {
			f := newDirectFixture(t)
			ref := f.grants[0]
			g := loadDirect[state.Grant](t, f.db, state.Key(state.KeyGrant, ref.Key.Encode()))
			c := protocol.ReserveIncrease{Network: f.org.Network, Organization: g.Organization, Key: g.Key, Grant: g.ID, Previous: g.Amount, Amount: 100}
			c.Sign(f.owner)
			sourceID := protocol.ReserveFundingAccount(c.Network, c.Subject)
			if same {
				g.Key.Account = sourceID
				c.Key = g.Key
				c.Sign(f.owner)
			}
			source := AccountKey(sourceID, protocol.AssetCAL)
			destination := AccountKey(c.Key.Account, protocol.AssetCAL)
			key := state.Key(state.KeyGrant, c.Key.Encode())
			if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				if err := state.Put(o, source, uint64(1000)); err != nil {
					return nil, err
				}
				if err := state.Put(o, key, g); err != nil {
					return nil, err
				}
				return o.Changes(), nil
			}); err != nil {
				t.Fatal(err)
			}
			before := loadDirect[uint64](t, f.db, destination)
			err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
				tr, err := EvaluateReserveIncrease(v, c, map[string]struct{}{string(source): {}, string(destination): {}})
				if err != nil && len(tr.Changes) != 0 {
					t.Fatal("rejection returned partial changes")
				}
				return tr.Changes, err
			})
			if err == nil {
				t.Fatal("protected source accepted")
			}
			if loadDirect[uint64](t, f.db, source) != 1000 || loadDirect[uint64](t, f.db, destination) != before || loadDirect[state.Grant](t, f.db, key) != g {
				t.Fatal("rejected command changed balances or grant")
			}
			id := c.ID()
			if err := f.db.View(func(v state.ReadView) error {
				_, found, err := state.Load[bool](v, state.Key(160, id[:]))
				if found {
					t.Fatal("rejected command marked seen")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

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
			tr, e := EvaluateReserveIncrease(v, c, map[string]struct{}{string(AccountKey(c.Key.Account, protocol.AssetCAL)): {}})
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
