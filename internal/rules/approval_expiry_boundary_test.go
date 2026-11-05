package rules

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func expiryRuleRows(t *testing.T, f directFixture) any {
	t.Helper()
	rows, err := store.Scan(f.db, nil, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func expiryAccounting(t *testing.T, f directFixture, parent, child DirectPayment, phase string, at int64) {
	t.Helper()
	var fees []directPayment
	for _, p := range []DirectPayment{parent, child} {
		var got directPayment
		var found bool
		if err := f.db.View(func(v state.ReadView) error {
			var err error
			got, found, err = state.Load[directPayment](v, state.Key(keyDirectPayment, p.Certificate.QC.Fact[:]))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if found {
			if err := got.Fee.Validate(); err != nil {
				t.Fatal(err)
			}
			fees = append(fees, got)
		}
	}
	fuel := loadDirect[uint64](t, f.db, AccountKey(f.org.Org, protocol.AssetFUEL))
	expected := uint64(10000000)
	for _, p := range fees {
		expected -= p.Fee.Held + p.Fee.Rewards + p.Fee.Burned
	}
	if fuel != expected {
		t.Fatal("FUEL account/escrow conservation", fuel, expected)
	}
	coverage := loadDirect[directCoverage](t, f.db, state.Key(keyDirectCoverage, parent.Certificate.QC.Fact[:]))
	ob := loadDirect[DirectObligation](t, f.db, DirectObligationKey(parent.Certificate.Summary.OutputID(0)))
	rec := map[string]any{"case_id": t.Name(), "phase": phase, "block_time": at, "deadline": parent.Tx.Body.ApprovalDeadline, "transaction": fmt.Sprintf("%x", parent.Tx.ID()), "certificate": fmt.Sprintf("%x", parent.Certificate.QC.Fact), "coverage": coverage.Credit, "obligation": ob, "fees": fees, "cal_balance": loadDirect[uint64](t, f.db, AccountKey(f.org.Org, protocol.AssetCAL)), "fuel_balance": fuel, "fuel_expected": expected}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("EXPIRY_CASE " + string(b))
}

func TestApprovalExpiryBoundaryWholeCertificateAccounting(t *testing.T) {
	const start int64 = 1700000000
	const deadline = start + protocol.ApprovalLifetimeSeconds
	for _, compensate := range []bool{false, true} {
		t.Run(map[bool]string{false: "fulfilled", true: "compensated_recovered"}[compensate], func(t *testing.T) {
			f := newDirectFixture(t)
			base := f.payment(t, f.genesis, nil, 1)
			body := base.Tx.Body
			body.ApprovalDeadline = deadline
			body.Outputs = []protocol.Output{f.output, f.output}
			body.Outputs[0].Amount = 40
			body.Outputs[1].Amount = 60
			tx, err := protocol.NewFastTx(body, base.Tx.Claims, f.policy.Key)
			if err != nil {
				t.Fatal(err)
			}
			tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
			parent := f.certify(t, tx, nil)
			childBody := f.payment(t, f.genesis, nil, 2).Tx.Body
			childBody.ApprovalDeadline = deadline + 100
			childBody.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: parent.Certificate.Summary.OutputID(0), Evidence: protocol.Hash(parent.Certificate.QC.Fact)}}
			childBody.Outputs = []protocol.Output{parent.Tx.Body.Outputs[0]}
			childTx, err := protocol.NewFastTx(childBody, []protocol.InputClaim{{Output: parent.Tx.Body.Outputs[0]}}, f.policy.Key)
			if err != nil {
				t.Fatal(err)
			}
			childTx.Auth = []protocol.OwnerAuth{protocol.SignOwner(childTx.ID(), f.owner)}
			child := f.certify(t, childTx, []InputCertificate{{Certificate: parent.Certificate, Index: 0}})
			f.settle(t, child, deadline-1)
			cov := loadDirect[directCoverage](t, f.db, state.Key(keyDirectCoverage, parent.Certificate.QC.Fact[:]))
			if cov.Credit.Original != 100 || cov.Credit.Remaining != 100 {
				t.Fatal("only used output registered instead of whole certificate", cov)
			}
			if ob := loadDirect[DirectObligation](t, f.db, DirectObligationKey(parent.Certificate.Summary.OutputID(0))); ob.Status != DirectOpen || ob.Amount != 40 || ob.Deadline != deadline-1+f.policy.TimeoutSeconds {
				t.Fatal(ob)
			}
			if err := f.db.View(func(v state.ReadView) error {
				return CheckApprovalFirstUse(v, parent.Certificate.Summary, deadline+1, false)
			}); err != nil {
				t.Fatal("registered certificate expired", err)
			}
			expiryAccounting(t, f, parent, child, "registered_one_of_two_outputs", deadline-1)
			childOutput := loadDirect[state.Creation](t, f.db, DirectCreationKey(child.Certificate.Summary.OutputID(0), 0))
			repair := func(at int64) error {
				return f.db.Update(func(v state.ReadView) ([]state.Change, error) {
					tr, err := EvaluateDirectCompensation(v, parent.Certificate.Summary.OutputID(0), f.policy, at)
					return tr.Changes, err
				})
			}
			if compensate {
				before := expiryRuleRows(t, f)
				if err := repair(deadline + 28); err == nil {
					t.Fatal("compensation before obligation deadline")
				}
				if !reflect.DeepEqual(before, expiryRuleRows(t, f)) {
					t.Fatal("early compensation changed state")
				}
				if err := repair(deadline + 29); err != nil {
					t.Fatal(err)
				}
				if ob := loadDirect[DirectObligation](t, f.db, DirectObligationKey(parent.Certificate.Summary.OutputID(0))); ob.Status != DirectRepaired {
					t.Fatal(ob)
				}
				if bal := loadDirect[uint64](t, f.db, AccountKey(f.org.Org, protocol.AssetCAL)); bal != 9999960 {
					t.Fatal("CAL compensation debit", bal)
				}
				cov = loadDirect[directCoverage](t, f.db, state.Key(keyDirectCoverage, parent.Certificate.QC.Fact[:]))
				if cov.Credit.Paid != 40 || cov.Credit.Remaining != 60 {
					t.Fatal("unused output coverage lost", cov)
				}
				expiryAccounting(t, f, parent, child, "compensated_used_output_other_60_still_covered", deadline+29)
				before = expiryRuleRows(t, f)
				if err := repair(deadline + 30); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, expiryRuleRows(t, f)) {
					t.Fatal("duplicate compensation changed state")
				}
			}
			f.settle(t, parent, deadline+40)
			want := uint8(DirectFulfilled)
			if compensate {
				want = DirectRecovered
			}
			if ob := loadDirect[DirectObligation](t, f.db, DirectObligationKey(parent.Certificate.Summary.OutputID(0))); ob.Status != want {
				t.Fatal(ob)
			}
			if bal := loadDirect[uint64](t, f.db, AccountKey(f.org.Org, protocol.AssetCAL)); bal != 10000000 {
				t.Fatal("CAL repayment/fulfillment balance", bal)
			}
			cov = loadDirect[directCoverage](t, f.db, state.Key(keyDirectCoverage, parent.Certificate.QC.Fact[:]))
			if cov.Credit.Remaining != 0 {
				t.Fatal("coverage not discharged", cov)
			}
			if compensate {
				if cov.Credit.Paid != 40 || cov.Credit.Recovered != 40 || cov.Credit.Discharged != 60 {
					t.Fatal(cov)
				}
			} else if cov.Credit.Discharged != 100 {
				t.Fatal(cov)
			}
			if got := loadDirect[state.Creation](t, f.db, DirectCreationKey(child.Certificate.Summary.OutputID(0), 0)); !reflect.DeepEqual(got, childOutput) {
				t.Fatal("late source altered successor output")
			}
			expiryAccounting(t, f, parent, child, "late_source_closed_obligation", deadline+40)
			before := expiryRuleRows(t, f)
			f.settle(t, parent, deadline+41)
			f.settle(t, child, deadline+41)
			if !reflect.DeepEqual(before, expiryRuleRows(t, f)) {
				t.Fatal("repeat settlement changed accounts")
			}
		})
	}
}

func TestApprovalExpiryBoundaryRealUTXONewCertificate(t *testing.T) {
	const deadline int64 = 1700000000 + protocol.ApprovalLifetimeSeconds
	f := newDirectFixture(t)
	parent := withApprovalDeadline(t, f, f.payment(t, f.genesis, nil, 1), deadline)
	f.settle(t, parent, deadline-1)
	child := withApprovalDeadline(t, f, f.payment(t, parent.Certificate.Summary.OutputID(0), &parent.Certificate, 2), deadline+100)
	f.settle(t, child, deadline+1)
	for _, p := range []DirectPayment{parent, child} {
		if err := f.db.View(func(v state.ReadView) error {
			if _, err := v.Get(state.Key(keyDirectCoverage, p.Certificate.QC.Fact[:])); err != state.ErrNotFound {
				t.Fatal("real output activated source coverage", err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	got := loadDirect[state.Creation](t, f.db, DirectCreationKey(child.Certificate.Summary.OutputID(0), 0))
	if !got.Final || got.Output.Amount != 100 {
		t.Fatal(got)
	}
	b, _ := json.Marshal(map[string]any{"case_id": t.Name(), "phase": "real_utxo_spent_after_source_expiry", "deadline": deadline, "block_time": deadline + 1, "output": got, "source_obligation_created": false})
	t.Log("EXPIRY_CASE " + string(b))
}
