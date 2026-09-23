package rules

import (
	"testing"
	"utxo/internal/state"
	"utxo/protocol"
)

// This deterministic ledger test controls order explicitly. It is not a
// replacement for the planned multi-process, two-organization network case.
func TestDirectTwoOrganizationsGrandchildFirst(t *testing.T) {
	for _, repairedBeforeParent := range []bool{false, true} {
		name := "parent-before-repair"
		if repairedBeforeParent {
			name = "parent-after-repair"
		}
		t.Run(name, func(t *testing.T) {
			g0, g1 := newDirectFixture(t), newDirectFixture(t)
			g1.db, g1.owner = g0.db, g0.owner
			g1.org.Org = protocol.Digest("E3-second-org")
			g1.output.Recipient = protocol.NewDescriptor(g1.org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: g1.org.Org}, g1.owner)
			g0.policy.Organizations[g1.org.Hash()] = g1.org
			g1.policy = g0.policy
			g1.grants = nil
			for _, ref := range g0.grants {
				if ref.Key.Kind == protocol.ResourceFUEL || ref.Key.Kind == protocol.ResourcePolicy {
					continue
				}
				ref.Key.Account = g1.org.Org
				ref.Grant = protocol.Digest("E3-grant", ref.Key.Encode())
				g1.grants = append(g1.grants, ref)
			}
			if err := g0.db.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				for _, ref := range g1.grants {
					if err := state.Put(o, state.Key(state.KeyGrant, ref.Key.Encode()), state.Grant{ID: ref.Grant, Organization: g1.org.Hash(), Key: ref.Key, Amount: 10000000}); err != nil {
						return nil, err
					}
				}
				if err := state.Put(o, AccountKey(g1.org.Org, protocol.AssetCAL), uint64(10000000)); err != nil {
					return nil, err
				}
				return o.Changes(), nil
			}); err != nil {
				t.Fatal(err)
			}
			payment := func(f directFixture, input protocol.OutputID, coin protocol.Output, parent *protocol.OutputCertificate, recipient protocol.ReceiveDescriptor, nonce byte) DirectPayment {
				t.Helper()
				in := protocol.Input{Kind: protocol.FinalInput, Output: input, Evidence: protocol.Digest("genesis-fact")}
				var parents []InputCertificate
				if parent != nil {
					in.Kind, in.Evidence = protocol.CertificateInput, protocol.Hash(parent.QC.Fact)
					parents = []InputCertificate{{Certificate: *parent, Index: 0}}
				}
				body := protocol.TxBody{Wire: 4, Version: 4, Network: f.org.Network, Kind: protocol.FastTransfer, Subject: coin.Recipient.Owner, Certifier: f.org.Org, Config: f.org.Hash(), Epoch: 1, Rules: f.policy.Rules(), Inputs: []protocol.Input{in}, Outputs: []protocol.Output{{Asset: protocol.AssetCAL, Amount: 100, Recipient: recipient}}, Work: protocol.WorkLimit{Execution: 100000, Bytes: 10000000, Depth: 1, Ancestors: 1}}
				body.Nonce[0] = nonce
				for _, ref := range f.grants {
					if ref.Key.Kind != protocol.ResourceFUEL && ref.Key.Kind != protocol.ResourcePolicy {
						body.Admission = append(body.Admission, ref)
					}
				}
				feeID := protocol.OutputID(protocol.Digest("E3-user-fee", []byte{nonce}))
				feeFact := protocol.Digest("E3-final-fee", feeID[:])
				if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
					o := state.NewOverlay(v)
					err := state.Put(o, DirectCreationKey(feeID, 0), state.Creation{Output: protocol.Output{Asset: protocol.AssetFUEL, Amount: 10000, Recipient: coin.Recipient}, Fact: feeFact, Final: true})
					return o.Changes(), err
				}); err != nil {
					t.Fatal(err)
				}
				body.Fee = protocol.FeeTerms{Source: protocol.OwnerFinalUTXO, Maximum: 1000, Inputs: []protocol.Input{{Kind: protocol.FinalInput, Output: feeID, Evidence: feeFact}}, Refund: coin.Recipient}
				body.Intent = body.IntentID()
				tx, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: coin}}, f.policy.Key)
				if err != nil {
					t.Fatal(err)
				}
				tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
				return f.certify(t, tx, parents)
			}
			t0 := payment(g0, g0.genesis, g0.output, nil, g1.output.Recipient, 1)
			o0 := t0.Certificate.Summary.OutputID(0)
			t1 := payment(g1, o0, t0.Tx.Body.Outputs[0], &t0.Certificate, g0.output.Recipient, 2)
			o1 := t1.Certificate.Summary.OutputID(0)
			t2 := payment(g0, o1, t1.Tx.Body.Outputs[0], &t1.Certificate, g0.output.Recipient, 3)
			g0.settle(t, t2, 100)
			ob := loadDirect[DirectObligation](t, g0.db, DirectObligationKey(o1))
			if ob.Issuer != g1.org.Org || ob.Status != DirectOpen {
				t.Fatalf("wrong direct issuer: %+v", ob)
			}
			repair := func(output protocol.OutputID, now int64) {
				t.Helper()
				if err := g0.db.Update(func(v state.ReadView) ([]state.Change, error) {
					tr, err := EvaluateDirectCompensation(v, output, g0.policy, now)
					return tr.Changes, err
				}); err != nil {
					t.Fatal(err)
				}
			}
			at, expectedG1 := int64(102), uint64(10000000)
			if repairedBeforeParent {
				repair(o1, 130)
				at, expectedG1 = 132, 9999900
			}
			g1.settle(t, t1, at)
			first := loadDirect[DirectObligation](t, g0.db, DirectObligationKey(o1))
			ancestor := loadDirect[DirectObligation](t, g0.db, DirectObligationKey(o0))
			want := uint8(DirectFulfilled)
			if repairedBeforeParent {
				want = DirectRepaired
			}
			if first.Issuer != g1.org.Org || first.Status != want || ancestor.Issuer != g0.org.Org || ancestor.Status != DirectOpen {
				t.Fatal("independent direct obligations were transferred or erased")
			}
			repair(o0, at+30)
			repair(o0, at+31)
			if loadDirect[uint64](t, g0.db, AccountKey(g0.org.Org, protocol.AssetCAL)) != 9999900 || loadDirect[uint64](t, g0.db, AccountKey(g1.org.Org, protocol.AssetCAL)) != expectedG1 {
				t.Fatal("wrong or duplicate reserve debit")
			}
			if !loadDirect[state.Creation](t, g0.db, DirectCreationKey(t2.Certificate.Summary.OutputID(0), 0)).Final {
				t.Fatal("grandchild was rolled back")
			}
		})
	}
}
