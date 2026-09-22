package budgetprobe

import (
	"testing"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestSnapshotSeparatesSpentFromRecyclableReservation(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	key := protocol.ResourceKey{Kind: protocol.ResourceFUEL, Version: 1}
	fact := protocol.SpendFactID{1}
	g := state.Grant{Key: key, Amount: 1500}
	err := db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		for _, x := range []struct {
			k []byte
			v any
		}{
			{state.Key(state.KeyApproval, fact[:]), state.Approval{Fact: fact, Direct: &protocol.FastTx{}, Debits: []state.Debit{{Key: key, Cap: 1000}}}},
			{member.ProgressKey(fact), member.LocalProgress{Settled: true, Fee: rules.Escrow{Maximum: 1000, Rewards: 84, Burned: 10, Refunded: 906, Closed: true}, Applied: []uint64{906}}},
			{state.SliceKey(key, 0), state.Slice{Available: 906, Reserved: 94}},
		} {
			if e := state.Put(o, x.k, x.v); e != nil {
				return nil, e
			}
		}
		return o.Changes(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Read(db, []state.Grant{g}, protocol.Hash{}, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	d := s.Detail.Debits[0]
	if d.Charged != 1000 || d.Released != 906 || d.NetSpent != 94 || s.Resources[0].Slices[0].Reserved != 94 {
		t.Fatalf("snapshot=%+v debit=%+v", s, d)
	}
	if d.Charged-d.Released-d.NetSpent != 0 {
		t.Fatal("spent fee reported as temporary occupancy")
	}
}

func TestSnapshotUnsettledApprovalRetainsFullDebit(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	key := protocol.ResourceKey{Kind: protocol.ResourceCAL}
	fact := protocol.SpendFactID{2}
	if e := db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		e := state.Put(o, state.Key(state.KeyApproval, fact[:]), state.Approval{Fact: fact, Direct: &protocol.FastTx{}, Debits: []state.Debit{{Key: key, Cap: 100}}})
		return o.Changes(), e
	}); e != nil {
		t.Fatal(e)
	}
	s, e := Read(db, []state.Grant{{Key: key}}, protocol.Hash{}, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	d := s.Detail.Debits[0]
	if d.Charged != 100 || d.Released != 0 || d.NetSpent != 0 || s.Detail.Settled != 0 {
		t.Fatalf("partial approval incorrectly cleared: %+v", s.Detail)
	}
}
