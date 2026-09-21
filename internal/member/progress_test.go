package member

import (
	"errors"
	"testing"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestDirectStatusIndependentFacts(t *testing.T) {
	for _, signed := range []bool{false, true} {
		for _, closed := range []bool{false, true} {
			db := store.NewMemory()
			m := &Member{db: db}
			var fact protocol.SpendFactID
			fact[0] = 1
			err := db.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				if signed {
					if e := state.Put(o, state.Key(state.KeyApproval, fact[:]), state.Approval{Fact: fact}); e != nil {
						return nil, e
					}
				}
				if e := state.Put(o, state.Key(state.KeyObserved, fact[:]), true); e != nil {
					return nil, e
				}
				if e := state.Put(o, ProgressKey(fact), LocalProgress{Height: 7, Fee: rules.Escrow{Closed: closed}}); e != nil {
					return nil, e
				}
				return o.Changes(), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := m.DirectStatus(fact)
			if err != nil || !got.Observed || got.Signed != signed || got.Closed != closed || got.Height != 7 {
				t.Fatalf("%+v %v", got, err)
			}
			absent, err := m.DirectStatus(protocol.SpendFactID{})
			if err != nil || absent != (DirectStatus{}) {
				t.Fatalf("absent: %+v %v", absent, err)
			}
			db.Close()
			if _, err := m.DirectStatus(fact); !errors.Is(err, store.ErrClosed) {
				t.Fatalf("lost read failure: %v", err)
			}
		}
	}
}

func BenchmarkDirectStatus(b *testing.B) {
	db := store.NewMemory()
	defer db.Close()
	m := &Member{db: db}
	var fact protocol.SpendFactID
	fact[0] = 1
	approval := state.Approval{Fact: fact, Direct: &protocol.FastTx{Body: protocol.TxBody{Inputs: make([]protocol.Input, 1), Outputs: make([]protocol.Output, 1)}, Auth: make([]protocol.OwnerAuth, 1)}, Debits: make([]state.Debit, 5)}
	if err := db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if err := state.Put(o, state.Key(state.KeyApproval, fact[:]), approval); err != nil {
			return nil, err
		}
		return o.Changes(), nil
	}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s, err := m.DirectStatus(fact)
		if err != nil || !s.Signed {
			b.Fatal(s, err)
		}
	}
}
