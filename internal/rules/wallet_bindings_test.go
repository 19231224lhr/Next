package rules

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestGenesisWalletBindingsMergeAndRemainImmutable(t *testing.T) {
	f := newDirectFixture(t)
	_, recipientKey, _ := ed25519.GenerateKey(rand.Reader)
	recipient := protocol.NewDescriptor(f.org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: protocol.Digest("recipient-org")}, recipientKey)
	gen := state.Genesis{Network: f.org.Network, Outputs: []state.OriginOutput{{ID: f.genesis, Output: f.output}}, Wallets: []state.WalletBinding{{Owner: f.output.Recipient.Owner, Organization: f.org.Org}, {Owner: recipient.Owner, Organization: recipient.Route.Org}}}
	if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		err := InitializeWalletBindings(o, gen)
		return o.Changes(), err
	}); err != nil {
		t.Fatal(err)
	}
	if got := loadDirect[protocol.Hash](t, f.db, state.Key(state.KeyWalletBinding, recipient.Owner[:])); got != recipient.Route.Org {
		t.Fatal("wallet without genesis funds was not bound")
	}
	if err := f.db.View(func(v state.ReadView) error {
		o := state.NewOverlay(v)
		gen.Wallets[1].Organization = f.org.Org
		if err := InitializeWalletBindings(o, gen); err == nil || len(o.Changes()) != 0 {
			t.Fatal("existing wallet binding was replaced or partly applied")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGenesisWalletBindingsRejectContradictionsAndInvalidValues(t *testing.T) {
	f := newDirectFixture(t)
	for _, scenario := range []string{"zero-owner", "zero-org", "explicit-conflict", "origin-conflict", "existing-conflict", "invalid-origin", "zero-network"} {
		t.Run(scenario, func(t *testing.T) {
			db := store.NewMemory()
			defer db.Close()
			binding := state.WalletBinding{Owner: f.output.Recipient.Owner, Organization: f.org.Org}
			gen := state.Genesis{Network: f.org.Network, Wallets: []state.WalletBinding{binding}}
			other := protocol.Digest("contradictory-org")
			switch scenario {
			case "zero-owner":
				gen.Wallets[0].Owner = protocol.PublicKey{}
			case "zero-org":
				gen.Wallets[0].Organization = protocol.Hash{}
			case "explicit-conflict":
				gen.Wallets = append(gen.Wallets, state.WalletBinding{Owner: binding.Owner, Organization: other})
			case "origin-conflict":
				out := f.output
				out.Recipient = protocol.NewDescriptor(f.org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: other}, f.owner)
				gen.Outputs = []state.OriginOutput{{Output: out}}
			case "existing-conflict":
				if err := db.Update(func(v state.ReadView) ([]state.Change, error) {
					o := state.NewOverlay(v)
					err := state.Put(o, state.Key(state.KeyWalletBinding, binding.Owner[:]), other)
					return o.Changes(), err
				}); err != nil {
					t.Fatal(err)
				}
			case "invalid-origin":
				out := f.output
				out.Recipient.Signature[0] ^= 1
				gen.Outputs = []state.OriginOutput{{Output: out}}
			case "zero-network":
				gen.Network = protocol.Hash{}
			}
			if err := db.View(func(v state.ReadView) error {
				o := state.NewOverlay(v)
				if err := InitializeWalletBindings(o, gen); err == nil || len(o.Changes()) != 0 {
					t.Fatal("invalid genesis binding accepted or partly applied")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDirectReplayRechecksWalletBindings(t *testing.T) {
	f := newDirectFixture(t)
	pay := f.payment(t, f.genesis, nil, 33)
	verified, err := VerifyDirectPayment(pay, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	f.settle(t, pay, 10)
	if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		o.Delete(state.Key(state.KeyWalletBinding, f.output.Recipient.Owner[:]))
		return o.Changes(), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.View(func(v state.ReadView) error {
		tr, err := EvaluateDirectPaymentAt(v, verified, f.policy, 11, 2)
		if err == nil || len(tr.Changes) != 0 {
			t.Fatal("settled or verified cache bypassed wallet bindings")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
