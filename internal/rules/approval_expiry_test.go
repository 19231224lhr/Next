package rules

import (
	"testing"
	"utxo/internal/state"
	"utxo/protocol"
)

func withApprovalDeadline(t *testing.T, f directFixture, pay DirectPayment, deadline int64) DirectPayment {
	t.Helper()
	body := pay.Tx.Body
	body.ApprovalDeadline = deadline
	tx, err := protocol.NewFastTx(body, pay.Tx.Claims, f.policy.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
	return f.certify(t, tx, pay.InputCertificates)
}

func TestApprovalExpiryRequiredGenesisRejectsMissingDeadline(t *testing.T) {
	f := newDirectFixture(t)
	p := f.payment(t, f.genesis, nil, 1)
	oldRules := f.policy.Rules()
	f.policy.RequireApprovalDeadline = true
	if f.policy.Rules() == oldRules {
		t.Fatal("genesis deadline policy not bound to rule identity")
	}
	if _, err := PrepareDirectVector(p.Tx, f.policy); err == nil {
		t.Fatal("missing D_T accepted in expiry-enabled network")
	}
}

func evaluateExpiryPayment(f directFixture, pay DirectPayment, now int64) error {
	verified, err := VerifyDirectPayment(pay, f.policy)
	if err != nil {
		return err
	}
	return f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, err := EvaluateDirectPayment(v, verified, f.policy, now)
		return tr.Changes, err
	})
}

func TestApprovalExpiryRejectsFirstUse(t *testing.T) {
	f := newDirectFixture(t)
	a := withApprovalDeadline(t, f, f.payment(t, f.genesis, nil, 1), 200)
	if err := evaluateExpiryPayment(f, a, 200); err == nil {
		t.Fatal("expired certificate executed for the first time")
	}
	b := f.payment(t, a.Certificate.Summary.OutputID(0), &a.Certificate, 2)
	if err := evaluateExpiryPayment(f, b, 200); err == nil {
		t.Fatal("expired source certificate established a new guarantee")
	}
}

func TestApprovalExpiryPreservesRegisteredGuarantee(t *testing.T) {
	for _, compensate := range []bool{false, true} {
		t.Run(map[bool]string{false: "fulfilled", true: "recovered"}[compensate], func(t *testing.T) {
			f := newDirectFixture(t)
			a := withApprovalDeadline(t, f, f.payment(t, f.genesis, nil, 1), 200)
			b := f.payment(t, a.Certificate.Summary.OutputID(0), &a.Certificate, 2)
			f.settle(t, b, 190)
			if compensate {
				if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
					tr, err := EvaluateDirectCompensation(v, a.Certificate.Summary.OutputID(0), f.policy, 230)
					return tr.Changes, err
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := evaluateExpiryPayment(f, a, 240); err != nil {
				t.Fatal("registered source rejected after expiry", err)
			}
		})
	}
}

func TestApprovalExpiryDoesNotExpireRealUTXO(t *testing.T) {
	f := newDirectFixture(t)
	a := withApprovalDeadline(t, f, f.payment(t, f.genesis, nil, 1), 200)
	f.settle(t, a, 190)
	b := f.payment(t, a.Certificate.Summary.OutputID(0), &a.Certificate, 2)
	if err := evaluateExpiryPayment(f, b, 240); err != nil {
		t.Fatal("real UTXO expired with its certificate", err)
	}
}
