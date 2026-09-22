package rules

import (
	"encoding/json"
	"reflect"
	"testing"
	"utxo/internal/state"
	"utxo/protocol"
)

func TestCloneDirectTxOwnsEverySlice(t *testing.T) {
	f := newDirectFixture(t)
	tx := f.payment(t, f.genesis, nil, 1).Tx
	// Fee inputs are empty in this payment mode; populate for the copy contract.
	tx.Body.Fee.Inputs = append([]protocol.Input(nil), tx.Body.Inputs...)
	before, _ := json.Marshal(tx)
	cloned := cloneDirectTx(tx)
	slices := 0
	var clearSlices func(reflect.Value)
	clearSlices = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				clearSlices(v.Field(i))
			}
		case reflect.Slice:
			slices++
			if v.Len() == 0 {
				t.Fatal("unexercised slice in transaction copy")
			}
			for i := 0; i < v.Len(); i++ {
				v.Index(i).SetZero()
			}
		}
	}
	clearSlices(reflect.ValueOf(&tx).Elem())
	if slices != 8 {
		t.Fatalf("copy contract changed: %d slices", slices)
	}
	after, _ := json.Marshal(cloned)
	if string(before) != string(after) {
		t.Fatal("caller mutation changed frozen transaction")
	}
}

func TestDirectSubmissionFrozenAfterCallerMutation(t *testing.T) {
	f := newDirectFixture(t)
	parent := f.payment(t, f.genesis, nil, 1)
	p := f.payment(t, parent.Certificate.Summary.OutputID(0), &parent.Certificate, 2).Submission()
	verified, err := VerifyDirectSubmission(p, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	var before, after state.Transition
	err = f.db.View(func(v state.ReadView) error {
		var e error
		before, e = EvaluateDirectPaymentAt(v, verified, f.policy, 100, 1)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	p.Tx.Body.Inputs[0].Output[0] ^= 1
	p.Tx.Body.Outputs[0].Amount++
	p.Tx.Body.Admission[0].Grant[0] ^= 1
	p.Tx.Claims[0].Output.Amount++
	p.Tx.Commitments[0][0] ^= 1
	p.Tx.Funding[0].Opening[0] ^= 1
	p.Tx.Auth[0].Signature[0] ^= 1
	p.Admission[0].Cap++
	p.Authorization.Votes[0].Signature[0] ^= 1
	p.InputCertificates[0].Certificate.Summary.Outputs[0].Amount++
	p.InputCertificates[0].Certificate.QC.Votes[0].Signature[0] ^= 1
	err = f.db.View(func(v state.ReadView) error {
		var e error
		after, e = EvaluateDirectPaymentAt(v, verified, f.policy, 100, 1)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("caller changed verified execution result")
	}
}

func TestDirectSubmissionCloneStillRejectsTampering(t *testing.T) {
	f := newDirectFixture(t)
	mutations := map[string]func(*protocol.DirectSubmission){
		"unused_refund":       func(p *protocol.DirectSubmission) { p.Tx.Body.Fee.Refund = f.output.Recipient },
		"owner_signature":     func(p *protocol.DirectSubmission) { p.Tx.Auth[0].Signature[0] ^= 1 },
		"recipient_signature": func(p *protocol.DirectSubmission) { p.Tx.Body.Outputs[0].Recipient.Signature[0] ^= 1 },
		"output_amount":       func(p *protocol.DirectSubmission) { p.Tx.Body.Outputs[0].Amount++ },
		"claim":               func(p *protocol.DirectSubmission) { p.Tx.Claims[0].Output.Amount++ },
		"funding":             func(p *protocol.DirectSubmission) { p.Tx.Funding[0].Opening[0] ^= 1 },
		"quorum":              func(p *protocol.DirectSubmission) { p.Authorization.Votes[0].Signature[0] ^= 1 },
		"work":                func(p *protocol.DirectSubmission) { p.Tx.Body.Work.Bytes = 0 },
		"fee":                 func(p *protocol.DirectSubmission) { p.Tx.Body.Fee.Maximum++ },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			p := f.payment(t, f.genesis, nil, 3).Submission()
			mutate(&p)
			if _, err := VerifyDirectSubmission(p, f.policy); err == nil {
				t.Fatal("tampered payment accepted")
			}
		})
	}
}

func TestDirectSubmissionCopyMatchesCanonicalFreeze(t *testing.T) {
	f := newDirectFixture(t)
	for _, empty := range []bool{false, true} {
		p := f.payment(t, f.genesis, nil, 9).Submission()
		if empty {
			p.Tx.Body.Fee.Inputs = []protocol.Input{}
		}
		raw, err := p.Tx.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := protocol.DecodeFastTx(raw)
		if err != nil {
			t.Fatal(err)
		}
		got, err := VerifyDirectSubmission(p, f.policy)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := got.payment.Tx.MarshalBinary()
		b, _ := legacy.MarshalBinary()
		if string(a) != string(b) || got.payment.Tx.ID() != legacy.ID() {
			t.Fatal("canonical wire/identity changed")
		}
		reference := p
		reference.Tx = legacy
		want, err := VerifyDirectSubmission(reference, f.policy)
		if err != nil {
			t.Fatal(err)
		}
		var x, y state.Transition
		err = f.db.View(func(v state.ReadView) error {
			var e error
			x, e = EvaluateDirectPaymentAt(v, got, f.policy, 100, 1)
			if e == nil {
				y, e = EvaluateDirectPaymentAt(v, want, f.policy, 100, 1)
			}
			return e
		})
		if err != nil || !reflect.DeepEqual(x, y) {
			t.Fatal("canonical freeze changed state", err)
		}
	}
}
