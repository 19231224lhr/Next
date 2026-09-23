package rules

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"utxo/internal/state"
	"utxo/protocol"
)

func seedOwnerFee(t *testing.T, f directFixture, label string, amount uint64) protocol.Input {
	t.Helper()
	in := protocol.Input{Kind: protocol.FinalInput, Output: protocol.OutputID(protocol.Digest("fee-input", []byte(label))), Evidence: protocol.Digest("fee-origin", []byte(label))}
	err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		out := f.output
		out.Asset = protocol.AssetFUEL
		out.Amount = amount
		err := state.Put(o, DirectCreationKey(in.Output, 0), state.Creation{Output: out, Fact: in.Evidence, Final: true})
		return o.Changes(), err
	})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func ownerPayment(t *testing.T, f directFixture, pay DirectPayment, fee protocol.Input) DirectPayment {
	t.Helper()
	b := pay.Tx.Body
	b.Fee = protocol.FeeTerms{Source: protocol.OwnerFinalUTXO, Maximum: 1000, Inputs: []protocol.Input{fee}, Refund: f.output.Recipient}
	b.Admission = nil
	for _, g := range f.grants {
		if g.Key.Kind != protocol.ResourceFUEL && g.Key.Kind != protocol.ResourcePolicy {
			b.Admission = append(b.Admission, g)
		}
	}
	b.Intent = b.IntentID()
	tx, err := protocol.NewFastTx(b, pay.Tx.Claims, f.policy.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
	return f.certify(t, tx, pay.InputCertificates)
}

func TestDirectOwnerFeeAtomicAccounting(t *testing.T) {
	f := newDirectFixture(t)
	fee := seedOwnerFee(t, f, "ordinary", 1500)
	pay := ownerPayment(t, f, f.payment(t, f.genesis, nil, 1), fee)
	for _, a := range pay.Certificate.Summary.Admission {
		if a.Key.Kind == protocol.ResourceFUEL || a.Key.Kind == protocol.ResourcePolicy {
			t.Fatal("owner fee charged an organization allocation")
		}
	}
	tr := f.settle(t, pay, 10)
	if len(tr.Changes) == 0 {
		t.Fatal("no payment changes")
	}
	err := f.db.View(func(v state.ReadView) error {
		balance, _, err := state.Load[uint64](v, AccountKey(f.org.Org, protocol.AssetFUEL))
		if err != nil {
			return err
		}
		if balance != 10000000 {
			t.Fatalf("organization paid: %d", balance)
		}
		spent, _, err := state.Load[state.Spend](v, DirectSpendKey(fee.Output, 0))
		if err != nil {
			return err
		}
		if spent.Consumed != pay.Certificate.QC.Fact {
			t.Fatal("fee input not consumed")
		}
		for index, amount := range map[uint32]uint64{protocol.MaxOutputs: 500, protocol.MaxOutputs + 1: 906} {
			id := protocol.OutputIdentity(f.org.Network, pay.Tx.ID(), index)
			c, ok, err := state.Load[state.Creation](v, DirectCreationKey(id, 0))
			if err != nil {
				return err
			}
			if !ok || !c.Final || c.Output.Asset != protocol.AssetFUEL || c.Output.Amount != amount || c.Output.Recipient != f.output.Recipient {
				t.Fatalf("bad owner change/refund %d: %+v", index, c)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.settle(t, pay, 11); len(got.Changes) != 0 {
		t.Fatal("duplicate payment changed accounting")
	}
}

func TestDirectOwnerFeeRejectsInvalidFundingAtomically(t *testing.T) {
	for _, which := range []string{"insufficient", "not-final", "wrong-asset", "wrong-owner", "wrong-route", "already-spent"} {
		t.Run(which, func(t *testing.T) {
			f := newDirectFixture(t)
			fee := seedOwnerFee(t, f, which, 1500)
			pay := ownerPayment(t, f, f.payment(t, f.genesis, nil, 2), fee)
			err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				k := DirectCreationKey(fee.Output, 0)
				c, _, err := state.Load[state.Creation](o, k)
				if err != nil {
					return nil, err
				}
				switch which {
				case "insufficient":
					c.Output.Amount = 999
				case "not-final":
					c.Final = false
				case "wrong-asset":
					c.Output.Asset = protocol.AssetCAL
				case "wrong-owner":
					_, key, _ := ed25519.GenerateKey(rand.Reader)
					c.Output.Recipient = protocol.NewDescriptor(f.org.Network, c.Output.Recipient.Route, key)
				case "wrong-route":
					c.Output.Recipient = protocol.NewDescriptor(f.org.Network, protocol.Route{Kind: protocol.CommitteeRoute}, f.owner)
				case "already-spent":
					if err = state.Put(o, DirectSpendKey(fee.Output, 0), state.Spend{Consumed: protocol.SpendFactID(protocol.Digest("other"))}); err != nil {
						return nil, err
					}
				}
				err = state.Put(o, k, c)
				return o.Changes(), err
			})
			if err != nil {
				t.Fatal(err)
			}
			v, err := VerifyDirectPayment(pay, f.policy)
			if err != nil {
				t.Fatal(err)
			}
			err = f.db.View(func(db state.ReadView) error {
				tr, e := EvaluateDirectPayment(db, v, f.policy, 10)
				if e == nil || len(tr.Changes) != 0 {
					t.Fatalf("invalid %s accepted or partial effects: %v", which, e)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDirectOwnerFeeRefundAfterParentArrives(t *testing.T) {
	f := newDirectFixture(t)
	parent := ownerPayment(t, f, f.payment(t, f.genesis, nil, 3), seedOwnerFee(t, f, "parent", 1500))
	child := ownerPayment(t, f, f.payment(t, parent.Certificate.Summary.OutputID(0), &parent.Certificate, 4), seedOwnerFee(t, f, "child", 1500))
	f.settle(t, child, 10)
	refund := protocol.OutputIdentity(f.org.Network, child.Tx.ID(), protocol.MaxOutputs+1)
	if err := f.db.View(func(v state.ReadView) error {
		_, ok, e := state.Load[state.Creation](v, DirectCreationKey(refund, 0))
		if ok {
			t.Fatal("refund before responsibility closure")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	f.settle(t, parent, 11)
	if err := f.db.View(func(v state.ReadView) error {
		c, ok, e := state.Load[state.Creation](v, DirectCreationKey(refund, 0))
		if !ok || c.Output.Amount != 906 {
			t.Fatalf("missing child refund: %+v", c)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDirectOwnerFeeRefundCanPayNextTransaction(t *testing.T) {
	f := newDirectFixture(t)
	parent := ownerPayment(t, f, f.payment(t, f.genesis, nil, 5), seedOwnerFee(t, f, "reuse", 1000))
	f.settle(t, parent, 10)
	refund := protocol.Input{Kind: protocol.FinalInput, Output: protocol.OutputIdentity(f.org.Network, parent.Tx.ID(), protocol.FeeRefundIndex), Evidence: protocol.CreationIdentity(f.org.Network, parent.Tx.ID(), protocol.FeeRefundIndex, 0)}
	next := ownerPayment(t, f, f.payment(t, parent.Certificate.Summary.OutputID(0), &parent.Certificate, 6), refund)
	body := next.Tx.Body
	body.Inputs[0] = protocol.Input{Kind: protocol.FinalInput, Output: parent.Certificate.Summary.OutputID(0), Evidence: protocol.CreationIdentity(f.org.Network, parent.Tx.ID(), 0, 0)}
	body.Fee.Maximum = 100
	body.Intent = body.IntentID()
	tx, err := protocol.NewFastTx(body, next.Tx.Claims, f.policy.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
	next = f.certify(t, tx, nil)
	f.settle(t, next, 11)
	if err = f.db.View(func(v state.ReadView) error {
		spent, _, e := state.Load[state.Spend](v, DirectSpendKey(refund.Output, 0))
		if spent.Consumed != next.Certificate.QC.Fact {
			t.Fatal("refund not spent as real FUEL")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}
