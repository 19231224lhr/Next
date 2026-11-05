package rules

import (
	"errors"
	"testing"

	"utxo/internal/state"
	"utxo/protocol"
)

// A wallet signature and a standing fee grant do not authorize bypassing the
// organization that has already locked this input for another fast payment.
func TestDirectPaymentRequiresCurrentOrganizationApproval(t *testing.T) {
	f := newDirectFixture(t)
	approved := f.payment(t, f.genesis, nil, 1)
	for _, votes := range []int{0, 1, 2} {
		candidate := f.payment(t, f.genesis, nil, byte(votes+2))
		candidate.Certificate.QC.Votes = candidate.Certificate.QC.Votes[:votes]
		if _, err := PrepareDirectVector(candidate.Tx, f.policy); err != nil {
			t.Fatal("test requires a valid owner-signed transaction:", err)
		}
		if _, err := VerifyDirectPayment(candidate, f.policy); !errors.Is(err, protocol.ErrAuth) {
			t.Fatalf("%d organization votes: got %v, want ErrAuth", votes, err)
		}
	}
	conflict := f.payment(t, f.genesis, nil, 9)
	conflict.Certificate = approved.Certificate
	if _, err := VerifyDirectPayment(conflict, f.policy); !errors.Is(err, protocol.ErrAuth) {
		t.Fatalf("approval for a different transaction accepted: %v", err)
	}
}

// Counterfactual, not a public entry point: construct the private verified value
// to model removing ONLY the current-payment QC gate. Every input, owner
// signature and fee grant remains valid. This documents why that deletion is
// not a safe optimization of the current payment protocol.
func TestRemovingCurrentQCAllowsForcedCompensation(t *testing.T) {
	f := newDirectFixture(t)
	promised := f.payment(t, f.genesis, nil, 1)
	id := promised.Certificate.Summary.OutputID(0)
	child := f.payment(t, id, &promised.Certificate, 2)
	bypass := f.payment(t, f.genesis, nil, 3)
	bypass.Certificate.QC.Votes = nil
	if _, err := PrepareDirectVector(bypass.Tx, f.policy); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyDirectPayment(bypass, f.policy); !errors.Is(err, protocol.ErrAuth) {
		t.Fatalf("production gate must reject the uncertified conflict: %v", err)
	}
	before := loadDirect[uint64](t, f.db, AccountKey(f.org.Org, protocol.AssetCAL))
	err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		unchecked := VerifiedDirectPayment{payment: bypass.Submission()}
		tr, err := EvaluateDirectPayment(v, unchecked, f.policy, 99)
		return tr.Changes, err
	})
	if err != nil {
		t.Fatal("counterfactual conflict was not admitted:", err)
	}
	f.settle(t, child, 100)
	verified, err := VerifyDirectPayment(promised, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	err = f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, err := EvaluateDirectPayment(v, verified, f.policy, 101)
		return tr.Changes, err
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("original certified payment should now conflict: %v", err)
	}
	err = f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, err := EvaluateDirectCompensation(v, id, f.policy, 100+f.policy.TimeoutSeconds)
		return tr.Changes, err
	})
	if err != nil {
		t.Fatal(err)
	}
	after := loadDirect[uint64](t, f.db, AccountKey(f.org.Org, protocol.AssetCAL))
	if before-after != f.output.Amount {
		t.Fatalf("expected forced reserve debit %d, got %d", f.output.Amount, before-after)
	}
	var available uint64
	for _, pay := range []DirectPayment{bypass, child} {
		out := pay.Certificate.Summary.OutputID(0)
		creation := loadDirect[state.Creation](t, f.db, DirectCreationKey(out, 0))
		if !creation.Final {
			t.Fatal("expected final output")
		}
		if err := f.db.View(func(v state.ReadView) error {
			spent, _, err := state.Load[state.Spend](v, DirectSpendKey(out, 0))
			if spent.Consumed != (protocol.SpendFactID{}) {
				t.Fatal("expected unspent output")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		available += creation.Output.Amount
	}
	if available != 2*f.output.Amount {
		t.Fatalf("unexpected final amount: %d", available)
	}
	t.Logf("counterfactual: initial input=%d, attacker final outputs=%d, organization reserve debit=%d", f.output.Amount, available, before-after)
}
