package rules

import (
	"bytes"
	"errors"
	"testing"

	"utxo/internal/state"
	"utxo/protocol"
)

func TestPublicSubmissionRetainsSpendAuthorizationWithoutNewTXCer(t *testing.T) {
	f := newDirectFixture(t)
	parent := f.payment(t, f.genesis, nil, 1)
	child := f.payment(t, parent.Certificate.Summary.OutputID(0), &parent.Certificate, 2)
	for _, payment := range []DirectPayment{parent, child} {
		public, err := payment.Submission().MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		internal, err := payment.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = protocol.DecodeDirectPayment(public); err == nil {
			t.Fatal("public format confused with internal TXCer delivery")
		}
		if _, err = protocol.DecodeDirectSubmission(internal); err == nil {
			t.Fatal("internal certificate accepted as public submission")
		}
		got, err := protocol.DecodeDirectSubmission(public)
		if err != nil {
			t.Fatal(err)
		}
		if got.Tx.ID() != payment.Tx.ID() || got.Authorization.Fact != payment.Certificate.QC.Fact || got.Summary().OutputID(0) != payment.Certificate.Summary.OutputID(0) {
			t.Fatal("transaction or output identity changed")
		}
		if _, err = VerifyDirectSubmission(got, f.policy); err != nil {
			t.Fatal(err)
		}
		again, err := got.MarshalBinary()
		if err != nil || !bytes.Equal(public, again) {
			t.Fatal("noncanonical public retry", err)
		}
		t.Logf("public=%d bytes, internal=%d bytes, input certificates=%d; existing organization signatures are retained", len(public), len(internal), len(got.InputCertificates))
		got.Authorization.Votes[0].Signature[0] ^= 1
		if _, err = VerifyDirectSubmission(got, f.policy); !errors.Is(err, protocol.ErrAuth) {
			t.Fatal("invalid organization authorization accepted", err)
		}
	}
}

func TestInputGuaranteeRegistrationHandlesSplitOutputsAndLateConsumer(t *testing.T) {
	for _, repair := range []bool{false, true} {
		t.Run(map[bool]string{false: "source_arrives", true: "partial_repair_then_source"}[repair], func(t *testing.T) {
			f := newDirectFixture(t)
			template := f.payment(t, f.genesis, nil, 1)
			body := template.Tx.Body
			body.Outputs = []protocol.Output{f.output, f.output}
			body.Outputs[0].Amount, body.Outputs[1].Amount = 40, 60
			body.Intent = body.IntentID()
			tx, err := protocol.NewFastTx(body, template.Tx.Claims, f.policy.Key)
			if err != nil {
				t.Fatal(err)
			}
			tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
			parent := f.certify(t, tx, nil)
			child := func(index uint32) DirectPayment {
				b := template.Tx.Body
				b.Nonce[0] = byte(10 + index)
				b.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: parent.Certificate.Summary.OutputID(index), Evidence: protocol.Hash(parent.Certificate.QC.Fact)}}
				b.Outputs = []protocol.Output{parent.Tx.Body.Outputs[index]}
				b.Intent = b.IntentID()
				tx, err := protocol.NewFastTx(b, []protocol.InputClaim{{Output: b.Outputs[0]}}, f.policy.Key)
				if err != nil {
					t.Fatal(err)
				}
				tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
				return f.certify(t, tx, []InputCertificate{{Certificate: parent.Certificate, Index: index}})
			}
			f.settle(t, child(0), 100)
			first := parent.Certificate.Summary.OutputID(0)
			if ob := loadDirect[DirectObligation](t, f.db, DirectObligationKey(first)); ob.Amount != 40 || ob.Status != DirectOpen {
				t.Fatal("incorrect immediate obligation", ob)
			}
			paid := uint64(0)
			if repair {
				paid = 40
				if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
					tr, err := EvaluateDirectCompensation(v, first, f.policy, 130)
					return tr.Changes, err
				}); err != nil {
					t.Fatal(err)
				}
			}
			f.settle(t, parent, 131)
			key := state.Key(keyDirectCoverage, parent.Certificate.QC.Fact[:])
			before := loadDirect[directCoverage](t, f.db, key).Credit
			if before.Discharged != 100-paid || before.Remaining != 0 || before.Paid != paid {
				t.Fatal("split promise not discharged", before)
			}
			f.settle(t, child(1), 132)
			after := loadDirect[directCoverage](t, f.db, key).Credit
			if before != after {
				t.Fatal("already-final input reopened coverage", before, after)
			}
			second := parent.Certificate.Summary.OutputID(1)
			f.db.View(func(v state.ReadView) error {
				if _, found, err := state.Load[DirectObligation](v, DirectObligationKey(second)); err != nil || found {
					t.Fatal("final input created an obligation", err)
				}
				return nil
			})
		})
	}
}

func TestPublicSubmissionRejectsMissingOrUnrelatedOrganizationApproval(t *testing.T) {
	f := newDirectFixture(t)
	a := f.payment(t, f.genesis, nil, 1)
	b := f.payment(t, f.genesis, nil, 2).Submission()
	b.Authorization = a.Certificate.QC
	if _, err := VerifyDirectSubmission(b, f.policy); !errors.Is(err, protocol.ErrAuth) {
		t.Fatal("unrelated approval accepted", err)
	}
	b = a.Submission()
	b.Authorization.Votes = nil
	if _, err := VerifyDirectSubmission(b, f.policy); !errors.Is(err, protocol.ErrAuth) {
		t.Fatal("missing approval accepted", err)
	}
}

func TestPublicSubmissionStillRequiresOwnerAuthorization(t *testing.T) {
	f := newDirectFixture(t)
	payment := f.payment(t, f.genesis, nil, 1)
	payment.Tx.Auth[0].Signature[0] ^= 1
	// OwnerAuth is outside the transaction identity: keep the valid organization
	// QC untouched, so this tests the independent owner verification boundary.
	if protocol.VerifyQC(payment.Certificate.QC, f.org) != nil {
		t.Fatal("test changed organization authorization")
	}
	if _, err := VerifyDirectSubmission(payment.Submission(), f.policy); err == nil {
		t.Fatal("public submission accepted invalid owner authorization")
	}
	if _, err := VerifyDirectPayment(payment, f.policy); err == nil {
		t.Fatal("internal installation accepted invalid owner authorization")
	}
}
