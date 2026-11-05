package committee

import (
	"testing"
	"time"

	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestDirectModeRejectsAlternatePaymentEnvelopes(t *testing.T) {
	cfg, f, _, payments := cacheFixture(t, 1)
	db := store.NewMemory()
	defer db.Close()
	e, err := NewEngine(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	payment, err := protocol.DecodeDirectSubmission(payments[0])
	if err != nil {
		t.Fatal(err)
	}
	bare, err := payment.Tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := f.Transaction(0, 1).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{bare, legacy} {
		if err := e.Check(raw); err == nil {
			t.Fatal("alternate envelope accepted by wire4 precheck")
		}
		if err := db.View(func(v state.ReadView) error {
			tr, err := e.ExecuteAt(v, raw, BlockContext{Height: 1, Time: time.Unix(100, 0)})
			if err == nil || len(tr.Changes) != 0 {
				t.Fatal("alternate envelope bypassed wire4 execution", err)
			}
			tr, err = e.Execute(v, raw)
			if err == nil || len(tr.Changes) != 0 {
				t.Fatal("legacy dispatcher bypassed configured direct mode", err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Check(payments[0]); err != nil {
		t.Fatal("authorized public submission rejected", err)
	}
	if err := db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, err := e.ExecuteAt(v, payments[0], BlockContext{Height: 1, Time: time.Unix(100, 0)})
		if err == nil && len(tr.Changes) == 0 {
			t.Fatal("authorized payment had no effects")
		}
		return tr.Changes, err
	}); err != nil {
		t.Fatal(err)
	}
}
