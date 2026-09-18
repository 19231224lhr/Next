package rules

import (
	"testing"
	"utxo/internal/state"
	"utxo/protocol"
)

func TestDirectIdleBlockCannotExpireNewPayment(t *testing.T) {
	f := newDirectFixture(t)
	parent := f.payment(t, f.genesis, nil, 11)
	output := parent.Certificate.Summary.OutputID(0)
	child := f.payment(t, output, &parent.Certificate, 12)
	verified, err := VerifyDirectPayment(child, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	// Block 10 is the first block after a long idle, with old Tendermint time.
	err = f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, err := EvaluateDirectPaymentAt(v, verified, f.policy, 100, 10)
		return tr.Changes, err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, err := EvaluateDirectCompensation(v, output, f.policy, 10000)
		return tr.Changes, err
	})
	if err == nil {
		t.Fatal("unanchored deadline paid")
	}
	err = f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, err := AnchorDirectDeadlines(v, f.policy, 11, 10000)
		return tr.Changes, err
	})
	if err != nil {
		t.Fatal(err)
	}
	ob := loadDirect[DirectObligation](t, f.db, DirectObligationKey(output))
	if ob.Deadline != 10030 {
		t.Fatalf("idle shortened deadline: %+v", ob)
	}
	err = f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, err := EvaluateDirectCompensation(v, output, f.policy, 10029)
		return tr.Changes, err
	})
	if err == nil {
		t.Fatal("early payment")
	}
	err = f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, err := EvaluateDirectCompensation(v, output, f.policy, 10030)
		return tr.Changes, err
	})
	if err != nil {
		t.Fatal(err)
	}
	r := loadDirect[protocol.CreditReceipt](t, f.db, DirectCALCreditKey(parent.Certificate.QC.Fact))
	if r.Paid != 100 {
		t.Fatal("deadline did not trigger payment")
	}
}
