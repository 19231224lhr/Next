package member

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestLocalCreditRetainsLegacyCumulativeAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "member.db")
	id := store.Identity{Network: "credit", Role: "member", Node: "0", Schema: DirectStoreSchema}
	db, err := store.Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	fact := protocol.SpendFactID(protocol.Digest("credit-test"))
	d := state.Debit{Key: protocol.ResourceKey{Kind: protocol.ResourceFUEL, Version: 1}, Cap: 100, Applied: 40, Worker: 7}
	a := state.Approval{Fact: fact, Direct: &protocol.FastTx{}, Debits: []state.Debit{d}}
	p := LocalProgress{Settled: true, Fee: rules.Escrow{Closed: true, Rewards: 10, Burned: 2}}
	err = db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if err := state.Put(o, state.Key(state.KeyApproval, fact[:]), a); err != nil {
			return nil, err
		}
		if err := state.Put(o, ProgressKey(fact), p); err != nil {
			return nil, err
		}
		if err := state.Put(o, state.SliceKey(d.Key, d.Worker), state.Slice{Available: 40, Reserved: 60}); err != nil {
			return nil, err
		}
		return o.Changes(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var original []byte
	if err := db.View(func(v state.ReadView) error {
		var err error
		original, err = v.Get(state.Key(state.KeyApproval, fact[:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		m := &Member{db: db}
		err = db.Update(func(v state.ReadView) ([]state.Change, error) {
			o := state.NewOverlay(v)
			a, _, err := state.Load[state.Approval](o, state.Key(state.KeyApproval, fact[:]))
			if err != nil {
				return nil, err
			}
			p, _, err := state.Load[LocalProgress](o, ProgressKey(fact))
			if err != nil {
				return nil, err
			}
			if err = m.finishLocal(o, &a, &p); err != nil {
				return nil, err
			}
			return o.Changes(), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		out, err := m.Outcome(fact)
		if err != nil || out.FuelResidual != 12 {
			t.Fatalf("round %d: %+v %v", round, out, err)
		}
		err = db.View(func(v state.ReadView) error {
			s, _, err := state.Load[state.Slice](v, state.SliceKey(d.Key, d.Worker))
			if err != nil {
				return err
			}
			if s.Available != 88 || s.Reserved != 12 {
				t.Fatalf("credit released twice or old cumulative lost: %+v", s)
			}
			current, err := v.Get(state.Key(state.KeyApproval, fact[:]))
			if !bytes.Equal(original, current) {
				t.Fatal("immutable approval changed")
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
		oldID := id
		oldID.Schema = 4
		if old, err := store.Open(path, oldID); !errors.Is(err, store.ErrIdentity) {
			if old != nil {
				old.Close()
			}
			t.Fatalf("old binary can ignore split credits: %v", err)
		}
		db, err = store.Open(path, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	defer db.Close()
	if _, err := localApplied(a, LocalProgress{Applied: []uint64{39}}); !errors.Is(err, rules.ErrAccounting) {
		t.Fatal("accepted cumulative below legacy value", err)
	}
	if _, err := localApplied(a, LocalProgress{Applied: []uint64{101}}); !errors.Is(err, rules.ErrAccounting) {
		t.Fatal("accepted cumulative above cap", err)
	}
	if _, err := localApplied(a, LocalProgress{Applied: []uint64{88, 0}}); !errors.Is(err, rules.ErrAccounting) {
		t.Fatal("accepted mismatched debit binding", err)
	}
}
