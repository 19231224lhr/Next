package rules

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"sort"
	"testing"

	"utxo/internal/state"
	"utxo/protocol"
)

func TestDirectPaymentRejectsWalletRouteViolationsAtomically(t *testing.T) {
	for _, scenario := range []string{"unknown-principal", "principal-other-org", "output-other-org", "output-committee", "unknown-output", "refund-other-org", "refund-committee"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDirectFixture(t)
			pay := f.payment(t, f.genesis, nil, 31)
			if scenario == "refund-other-org" || scenario == "refund-committee" {
				pay = ownerPayment(t, f, pay, seedOwnerFee(t, f, scenario, 1500))
			}
			body := pay.Tx.Body
			body.Outputs = append([]protocol.Output(nil), body.Outputs...)
			otherOrg := protocol.Digest("wallet-route-other-org")
			switch scenario {
			case "output-other-org":
				body.Outputs[0].Recipient = protocol.NewDescriptor(f.org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: otherOrg}, f.owner)
			case "output-committee":
				body.Outputs[0].Recipient = protocol.NewDescriptor(f.org.Network, protocol.Route{Kind: protocol.CommitteeRoute}, f.owner)
			case "unknown-output":
				_, unknown, _ := ed25519.GenerateKey(rand.Reader)
				body.Outputs[0].Recipient = protocol.NewDescriptor(f.org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: f.org.Org}, unknown)
			case "refund-other-org":
				body.Fee.Refund = protocol.NewDescriptor(f.org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: otherOrg}, f.owner)
			case "refund-committee":
				body.Fee.Refund = protocol.NewDescriptor(f.org.Network, protocol.Route{Kind: protocol.CommitteeRoute}, f.owner)
			}
			tx, err := protocol.NewFastTx(body, pay.Tx.Claims, f.policy.Key)
			if err != nil {
				t.Fatal(err)
			}
			tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
			pay = f.certify(t, tx, nil)
			verified, err := VerifyDirectPayment(pay, f.policy)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				org := f.org.Org
				if scenario == "principal-other-org" {
					org = otherOrg
				}
				if scenario == "unknown-principal" {
					o.Delete(state.Key(state.KeyWalletBinding, f.output.Recipient.Owner[:]))
				} else if err := state.Put(o, state.Key(state.KeyWalletBinding, f.output.Recipient.Owner[:]), org); err != nil {
					return nil, err
				}
				return o.Changes(), nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.db.View(func(v state.ReadView) error {
				tr, err := EvaluateDirectPaymentAt(v, verified, f.policy, 10, 1)
				if err == nil || len(tr.Changes) != 0 {
					t.Fatalf("invalid wallet route accepted or partial effects: %v", err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCommitteeTransferChecksWalletRoutes(t *testing.T) {
	for _, scenario := range []string{"valid", "unknown-principal", "output-other-org", "output-committee", "unknown-output", "refund-other-org", "refund-committee"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDirectFixture(t)
			committee := protocol.NewDescriptor(f.org.Network, protocol.Route{Kind: protocol.CommitteeRoute}, f.owner)
			principal := protocol.Input{Kind: protocol.FinalInput, Output: protocol.OutputID(protocol.Digest("committee-principal")), Evidence: protocol.Digest("committee-origin")}
			fee := protocol.Input{Kind: protocol.FinalInput, Output: protocol.OutputID(protocol.Digest("committee-fee")), Evidence: protocol.Digest("committee-fee-origin")}
			if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				for _, entry := range []struct {
					in     protocol.Input
					asset  protocol.Asset
					amount uint64
				}{{principal, protocol.AssetCAL, 100}, {fee, protocol.AssetFUEL, 1500}} {
					if err := state.Put(o, state.Key(state.KeyCreation, entry.in.Output[:]), state.Creation{Output: protocol.Output{Asset: entry.asset, Amount: entry.amount, Recipient: committee}, Fact: entry.in.Evidence, Final: true}); err != nil {
						return nil, err
					}
				}
				if scenario == "unknown-principal" {
					o.Delete(state.Key(state.KeyWalletBinding, f.output.Recipient.Owner[:]))
				}
				return o.Changes(), nil
			}); err != nil {
				t.Fatal(err)
			}
			body := protocol.TxBody{Wire: protocol.WireVersion, Version: protocol.ProtocolVersion, Network: f.org.Network, Kind: protocol.DirectTransfer, Subject: f.output.Recipient.Owner, Rules: f.policy.Base.IDs(), Inputs: []protocol.Input{principal}, Outputs: []protocol.Output{f.output}, Fee: protocol.FeeTerms{Source: protocol.OwnerFinalUTXO, Maximum: 1000, Inputs: []protocol.Input{fee}, Refund: f.output.Recipient}}
			other := protocol.Route{Kind: protocol.OrgRoute, Org: protocol.Digest("committee-other-org")}
			switch scenario {
			case "output-other-org":
				body.Outputs[0].Recipient = protocol.NewDescriptor(f.org.Network, other, f.owner)
			case "output-committee":
				body.Outputs[0].Recipient = committee
			case "unknown-output":
				_, unknown, _ := ed25519.GenerateKey(rand.Reader)
				body.Outputs[0].Recipient = protocol.NewDescriptor(f.org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: f.org.Org}, unknown)
			case "refund-other-org":
				body.Fee.Refund = protocol.NewDescriptor(f.org.Network, other, f.owner)
			case "refund-committee":
				body.Fee.Refund = committee
			}
			tx := protocol.SignedTx{Body: body, Auth: []protocol.OwnerAuth{protocol.SignOwner(body.ID(), f.owner)}}
			verified, err := VerifyDirect(tx, f.org.Network, f.policy.Base)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.db.View(func(v state.ReadView) error {
				tr, err := EvaluateDirectTransfer(v, verified, f.policy.Base)
				if scenario == "valid" {
					if err != nil || len(tr.Changes) == 0 {
						t.Fatalf("bound committee transfer rejected: %v", err)
					}
					return nil
				}
				if err == nil || len(tr.Changes) != 0 {
					t.Fatalf("committee transfer accepted invalid wallet route or partial effects: %v", err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDirectMultipleOwnersRequireSameOrganization(t *testing.T) {
	for _, sameOrg := range []bool{true, false} {
		name := "same-org"
		if !sameOrg {
			name = "different-org"
		}
		t.Run(name, func(t *testing.T) {
			f := newDirectFixture(t)
			_, secondOwner, _ := ed25519.GenerateKey(rand.Reader)
			_, subject, _ := ed25519.GenerateKey(rand.Reader)
			secondCoin := f.output
			secondCoin.Recipient = protocol.NewDescriptor(f.org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: f.org.Org}, secondOwner)
			var subjectPub protocol.PublicKey
			copy(subjectPub[:], subject.Public().(ed25519.PublicKey))
			secondID := protocol.OutputID(protocol.Digest("second-owner-principal"))
			if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				org := f.org.Org
				if !sameOrg {
					org = protocol.Digest("second-owner-org")
				}
				if err := InitializeWalletBindings(o, state.Genesis{Network: f.org.Network, Wallets: []state.WalletBinding{{Owner: secondCoin.Recipient.Owner, Organization: org}}}); err != nil {
					return nil, err
				}
				if err := state.Put(o, DirectCreationKey(secondID, 0), state.Creation{Output: secondCoin, Fact: protocol.Digest("genesis-fact"), Final: true}); err != nil {
					return nil, err
				}
				for _, ref := range f.grants {
					if ref.Key.Kind != protocol.ResourcePolicy {
						continue
					}
					grant, _, err := state.Load[state.Grant](v, state.Key(state.KeyGrant, ref.Key.Encode()))
					if err != nil {
						return nil, err
					}
					grant.Subject = subjectPub
					if err := state.Put(o, state.Key(state.KeyGrant, ref.Key.Encode()), grant); err != nil {
						return nil, err
					}
				}
				return o.Changes(), nil
			}); err != nil {
				t.Fatal(err)
			}
			body := f.payment(t, f.genesis, nil, 34).Tx.Body
			body.Subject = subjectPub
			body.Outputs = []protocol.Output{f.output}
			body.Outputs[0].Amount = 200
			body.Inputs = append(body.Inputs, protocol.Input{Kind: protocol.FinalInput, Output: secondID, Evidence: protocol.Digest("genesis-fact")})
			claims := []protocol.InputClaim{{Output: f.output}, {Output: secondCoin}}
			if bytes.Compare(body.Inputs[0].Output[:], body.Inputs[1].Output[:]) > 0 {
				body.Inputs[0], body.Inputs[1] = body.Inputs[1], body.Inputs[0]
				claims[0], claims[1] = claims[1], claims[0]
			}
			tx, err := protocol.NewFastTx(body, claims, f.policy.Key)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []ed25519.PrivateKey{f.owner, secondOwner, subject} {
				tx.Auth = append(tx.Auth, protocol.SignOwner(tx.ID(), key))
			}
			sort.Slice(tx.Auth, func(i, j int) bool { return bytes.Compare(tx.Auth[i].Owner[:], tx.Auth[j].Owner[:]) < 0 })
			pay := f.certify(t, tx, nil)
			verified, err := VerifyDirectPayment(pay, f.policy)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.db.View(func(v state.ReadView) error {
				tr, err := EvaluateDirectPayment(v, verified, f.policy, 10)
				if sameOrg && (err != nil || len(tr.Changes) == 0) {
					t.Fatalf("owners from the same organization rejected: %v", err)
				}
				if !sameOrg && (err == nil || len(tr.Changes) != 0) {
					t.Fatal("owners from different organizations accepted or partly applied")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateWalletRoutesRejectsUnsignedFeeOwner(t *testing.T) {
	f := newDirectFixture(t)
	_, feeOwner, _ := ed25519.GenerateKey(rand.Reader)
	feeRecipient := protocol.NewDescriptor(f.org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: f.org.Org}, feeOwner)
	fee := seedOwnerFee(t, f, "unsigned-fee-owner", 1500)
	pay := ownerPayment(t, f, f.payment(t, f.genesis, nil, 32), fee)
	if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if err := InitializeWalletBindings(o, state.Genesis{Network: f.org.Network, Wallets: []state.WalletBinding{{Owner: feeRecipient.Owner, Organization: f.org.Org}}}); err != nil {
			return nil, err
		}
		c, _, err := state.Load[state.Creation](v, DirectCreationKey(fee.Output, 0))
		if err != nil {
			return nil, err
		}
		c.Output.Recipient = feeRecipient
		err = state.Put(o, DirectCreationKey(fee.Output, 0), c)
		return o.Changes(), err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.View(func(v state.ReadView) error {
		if err := ValidateWalletRoutes(v, pay.Tx); err == nil {
			t.Fatal("fee input owner did not authorize the payment")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateDirectFeeRejectsContradictoryOwnerBinding(t *testing.T) {
	f := newDirectFixture(t)
	fee := seedOwnerFee(t, f, "contradictory-fee-binding", 1500)
	pay := ownerPayment(t, f, f.payment(t, f.genesis, nil, 35), fee)
	if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		err := state.Put(o, state.Key(state.KeyWalletBinding, f.output.Recipient.Owner[:]), protocol.Digest("wrong-fee-owner-org"))
		return o.Changes(), err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.View(func(v state.ReadView) error {
		if _, err := ValidateDirectFee(v, pay.Tx); err == nil {
			t.Fatal("fee owner routed outside its immutable organization")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
