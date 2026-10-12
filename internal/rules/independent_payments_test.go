package rules

import (
	"testing"

	"utxo/internal/state"
	"utxo/protocol"
)

func TestIndependentInputsWithSameSubjectNonceExecuteAndReplay(t *testing.T) {
	f := newDirectFixture(t)
	second := protocol.OutputID(protocol.Digest("independent-genesis-output"))
	if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		err := state.Put(o, DirectCreationKey(second, 0), state.Creation{Output: f.output, Fact: protocol.Digest("genesis-fact"), Final: true})
		return o.Changes(), err
	}); err != nil {
		t.Fatal(err)
	}
	firstPay := f.payment(t, f.genesis, nil, 7)
	secondPay := f.payment(t, second, nil, 7)
	if firstPay.Tx.Body.Subject != secondPay.Tx.Body.Subject || firstPay.Tx.Body.Nonce != secondPay.Tx.Body.Nonce || firstPay.Tx.ID() == secondPay.Tx.ID() {
		t.Fatal("test requires independent transactions with identical subject and nonce")
	}
	f.settle(t, firstPay, 10)
	f.settle(t, secondPay, 11)
	for _, pay := range []DirectPayment{firstPay, secondPay} {
		created := loadDirect[state.Creation](t, f.db, DirectCreationKey(pay.Certificate.Summary.OutputID(0), 0))
		if !created.Final || created.Output != f.output {
			t.Fatal("independent payment did not create its output")
		}
		if tr := f.settle(t, pay, 12); len(tr.Changes) != 0 {
			t.Fatal("transaction replay changed state")
		}
	}
}
