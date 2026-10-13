package rules

import (
	"crypto/ed25519"
	"testing"
	"utxo/internal/state"
	"utxo/protocol"
)

func c3Owner(t *testing.T, f directFixture, p DirectPayment, nonce byte) DirectPayment {
	t.Helper()
	id := protocol.OutputID(protocol.Digest("c3-owner-fee", []byte{nonce}))
	fact := protocol.Digest("c3-fee-final", id[:])
	coin := protocol.Output{Asset: protocol.AssetFUEL, Amount: 10000, Recipient: f.output.Recipient}
	if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		err := state.Put(o, DirectCreationKey(id, 0), state.Creation{Output: coin, Fact: fact, Final: true})
		return o.Changes(), err
	}); err != nil {
		t.Fatal(err)
	}
	b := p.Tx.Body
	b.Fee = protocol.FeeTerms{Source: protocol.OwnerFinalUTXO, Maximum: 10, Refund: coin.Recipient, Inputs: []protocol.Input{{Kind: protocol.FinalInput, Output: id, Evidence: fact}}}
	if err := BindC3(&b, f.policy, f.grants); err != nil {
		t.Fatal(err)
	}
	tx, err := protocol.NewFastTx(b, p.Tx.Claims, f.policy.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
	return f.certify(t, tx, p.InputCertificates)
}

func TestC3OwnerQuoteIndependentOfResolution(t *testing.T) {
	for _, compensate := range []bool{false, true} {
		t.Run(map[bool]string{false: "fulfilled", true: "compensated"}[compensate], func(t *testing.T) {
			f := c3Fixture(t)
			source := c3Owner(t, f, c3Payment(t, f, nil, 1), 1)
			child := c3Owner(t, f, c3Payment(t, f, &source.Certificate, 2), 2)
			f.settle(t, child, 100)
			if compensate {
				if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
					tr, e := EvaluateDirectCompensation(v, source.Certificate.Summary.OutputID(0), f.policy, 131)
					return tr.Changes, e
				}); err != nil {
					t.Fatal(err)
				}
			}
			f.settle(t, source, 132)
			for _, p := range []DirectPayment{source, child} {
				id := protocol.OutputIdentity(f.org.Network, p.Tx.ID(), protocol.FeeChangeIndex)
				if got := loadDirect[state.Creation](t, f.db, DirectCreationKey(id, 0)); got.Output.Amount != 9990 {
					t.Fatalf("variable quote %d", got.Output.Amount)
				}
				refund := protocol.OutputIdentity(f.org.Network, p.Tx.ID(), protocol.FeeRefundIndex)
				_ = f.db.View(func(v state.ReadView) error {
					if _, err := v.Get(DirectCreationKey(refund, 0)); err != state.ErrNotFound {
						t.Fatal("C3 produced variable refund")
					}
					return nil
				})
			}
			if got := loadDirect[uint64](t, f.db, AccountKey(source.Tx.Body.Fee.C3.Revenue, protocol.AssetFUEL)); got != 20 {
				t.Fatalf("revenue %d", got)
			}
		})
	}
}

func TestC3PolicyAuthenticationAndHistoricalTerms(t *testing.T) {
	f := c3Fixture(t)
	pay := c3Payment(t, f, nil, 1)
	next := C3Policy{ID: protocol.Digest("future"), Schedule: protocol.FeeSchedule{AttachReward: 20, ResolveReward: 10, Open: 4, Close: 4, Compensate: 4, Repay: 4}}
	f.policy.C3.Policies = append(f.policy.C3.Policies, next)
	f.policy.C3.Current = next.ID
	if _, err := VerifyDirectPayment(pay, f.policy); err != nil {
		t.Fatal("old bound policy reinterpreted", err)
	}
	for _, change := range []func(*protocol.TxBody){func(b *protocol.TxBody) { b.Fee.C3.Beneficiary = protocol.Digest("thief") }, func(b *protocol.TxBody) {
		b.Fee.C3.Schedule.Open++
		plan, _ := b.Fee.C3.Plan()
		b.Fee.C3.Budget = plan.Maximum
	}, func(b *protocol.TxBody) { b.Fee.C3 = protocol.FeeCommitment{} }} {
		b := pay.Tx.Body
		change(&b)
		tx, err := protocol.NewFastTx(b, pay.Tx.Claims, f.policy.Key)
		if err != nil {
			continue
		}
		tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
		if _, err = PrepareDirectVector(tx, f.policy); err == nil {
			t.Fatal("unauthenticated or omitted C3 accepted")
		}
	}
	malformed := pay.Certificate.Summary
	malformed.Grants = nil
	if ValidateC3Summary(malformed, f.policy) == nil {
		t.Fatal("malformed source accepted")
	}
}

func TestC3FeeInputMustMatchExplicitPayer(t *testing.T) {
	f := c3Fixture(t)
	pay := c3Owner(t, f, c3Payment(t, f, nil, 1), 1)
	otherSeed := protocol.Digest("other-owner")
	other := ed25519.NewKeyFromSeed(otherSeed[:])
	desc := protocol.NewDescriptor(f.org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: f.org.Org}, other)
	in := pay.Tx.Body.Fee.Inputs[0]
	if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if e := InitializeWalletBindings(o, state.Genesis{Network: f.org.Network, Wallets: []state.WalletBinding{{Owner: desc.Owner, Organization: f.org.Org}}}); e != nil {
			return nil, e
		}
		err := state.Put(o, DirectCreationKey(in.Output, 0), state.Creation{Output: protocol.Output{Asset: protocol.AssetFUEL, Amount: 10000, Recipient: desc}, Fact: in.Evidence, Final: true})
		return o.Changes(), err
	}); err != nil {
		t.Fatal(err)
	}
	pay.Tx.Auth = append(pay.Tx.Auth, protocol.SignOwner(pay.Tx.ID(), other))
	if err := f.db.View(func(v state.ReadView) error { _, err := ValidateDirectFee(v, pay.Tx); return err }); err == nil {
		t.Fatal("a second owner signature bypassed explicit payer")
	}
}

func TestC3MultiOutputDifferentQuorumSubsetAndLateRepay(t *testing.T) {
	f := c3Fixture(t)
	source := c3Payment(t, f, nil, 1)
	b := source.Tx.Body
	b.Outputs = []protocol.Output{f.output, f.output}
	b.Outputs[0].Amount, b.Outputs[1].Amount = 40, 60
	if e := BindC3(&b, f.policy, f.grants); e != nil {
		t.Fatal(e)
	}
	tx, e := protocol.NewFastTx(b, source.Tx.Claims, f.policy.Key)
	if e != nil {
		t.Fatal(e)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
	source = f.certify(t, tx, nil)
	makeChild := func(index uint32) DirectPayment {
		ff := f
		ff.output = b.Outputs[index]
		p := c3Payment(t, ff, &source.Certificate, byte(index+2))
		body := p.Tx.Body
		body.Inputs[0].Output = source.Certificate.Summary.OutputID(index)
		tx, e := protocol.NewFastTx(body, p.Tx.Claims, f.policy.Key)
		if e != nil {
			t.Fatal(e)
		}
		tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
		parent := source.Certificate
		if index == 1 {
			parent.QC.Votes = nil
			for _, i := range []uint16{0, 2, 3} {
				parent.QC.Votes = append(parent.QC.Votes, protocol.SignSpend(parent.QC.Fact, i, f.members[i]))
			}
		}
		return f.certify(t, tx, []InputCertificate{{Certificate: parent, Index: index}})
	}
	first, second := makeChild(0), makeChild(1)
	f.settle(t, first, 100)
	f.settle(t, second, 101)
	rec := loadDirect[C3Record](t, f.db, C3EscrowKey(source.Certificate.QC.Fact))
	if rec.Escrow.Commitment.Budget != 11 || rec.Escrow.Rewards != 1 || rec.Escrow.Burned != 2 {
		t.Fatalf("duplicate attachment %+v", rec)
	}
	if e = f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, e := EvaluateDirectCompensation(v, source.Certificate.Summary.OutputID(0), f.policy, 131)
		return tr.Changes, e
	}); e != nil {
		t.Fatal(e)
	}
	f.settle(t, source, 132)
	rec = loadDirect[C3Record](t, f.db, C3EscrowKey(source.Certificate.QC.Fact))
	if rec.Escrow.Rewards != 3 || rec.Escrow.Burned != 6 || rec.Escrow.Released != 2 || rec.Escrow.Held != 0 {
		t.Fatalf("multi output fee %+v", rec)
	}
}

func c3Fixture(t *testing.T) directFixture {
	f := newDirectFixture(t)
	pool := protocol.Digest("issuer-pool")
	ref := protocol.AdmissionRef{Key: protocol.ResourceKey{Kind: protocol.ResourceFUEL, Account: pool, Version: 1}, Grant: protocol.Digest("issuer-grant")}
	f.policy.C3 = &C3Settings{Current: protocol.Digest("c3-policy"), Policies: []C3Policy{{ID: protocol.Digest("c3-policy"), Schedule: protocol.FeeSchedule{AttachReward: 1, ResolveReward: 1, Open: 1, Close: 1, Compensate: 1, Repay: 1}}}, Issuers: []C3Issuer{{Config: f.org.Hash(), Funding: ref, Revenue: protocol.Digest("revenue"), Beneficiary: protocol.Digest("committee")}}}
	if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if e := state.Put(o, state.Key(state.KeyGrant, ref.Key.Encode()), state.Grant{ID: ref.Grant, Key: ref.Key, Organization: f.org.Hash(), Amount: 1000, Subject: f.output.Recipient.Owner}); e != nil {
			return nil, e
		}
		if e := state.Put(o, AccountKey(pool, protocol.AssetFUEL), uint64(1000)); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	}); err != nil {
		t.Fatal(err)
	}
	f.grants = append(f.grants, ref)
	return f
}

func c3Payment(t *testing.T, f directFixture, parent *protocol.OutputCertificate, n byte) DirectPayment {
	// Build legacy material with a legacy policy, then bind the explicit C3 plan before signing.
	legacy := f
	legacy.policy.C3 = nil
	legacy.grants = f.grants[:len(f.grants)-1]
	in := f.genesis
	if parent != nil {
		in = parent.Summary.OutputID(0)
	}
	p := legacy.payment(t, in, parent, n)
	b := p.Tx.Body
	if err := BindC3(&b, f.policy, f.grants); err != nil {
		t.Fatal(err)
	}
	tx, err := protocol.NewFastTx(b, p.Tx.Claims, f.policy.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
	return f.certify(t, tx, p.InputCertificates)
}

func TestC3PublicLifecycle(t *testing.T) {
	for _, path := range []string{"source-first", "child-first", "compensated-late"} {
		t.Run(path, func(t *testing.T) {
			f := c3Fixture(t)
			source := c3Payment(t, f, nil, 1)
			child := c3Payment(t, f, &source.Certificate, 2)
			if path == "source-first" {
				f.settle(t, source, 100)
			}
			f.settle(t, child, 101)
			if path == "compensated-late" {
				err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
					tr, e := EvaluateDirectCompensation(v, source.Certificate.Summary.OutputID(0), f.policy, 132)
					return tr.Changes, e
				})
				if err != nil {
					t.Fatal(err)
				}
				s := loadDirect[C3Record](t, f.db, C3EscrowKey(source.Certificate.QC.Fact))
				if s.QuoteCollected || s.Escrow.Held != 1 || s.Escrow.Burned != 3 {
					t.Fatalf("pre-execution %+v", s)
				}
			}
			if path != "source-first" {
				f.settle(t, source, 133)
			}
			s := loadDirect[C3Record](t, f.db, C3EscrowKey(source.Certificate.QC.Fact))
			want := uint64(0)
			if path == "child-first" {
				want = 2
			}
			if path == "compensated-late" {
				want = 4
			}
			if !s.QuoteCollected || s.Escrow.Rewards != 2 || s.Escrow.Burned != want || s.Escrow.Held != 0 || s.Escrow.Released != 4-want {
				t.Fatalf("fee state %+v", s)
			}
			revenue := loadDirect[uint64](t, f.db, AccountKey(source.Tx.Body.Fee.C3.Revenue, protocol.AssetFUEL))
			if revenue != 2000 {
				t.Fatalf("quote income %d", revenue)
			}
			if len(f.settle(t, source, 134).Changes) != 0 {
				t.Fatal("replay charged")
			}
		})
	}
}
