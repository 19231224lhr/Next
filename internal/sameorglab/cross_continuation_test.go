package sameorglab_test

import (
	"context"
	"crypto/ed25519"
	"testing"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func newCrossLabs(t *testing.T, v variant, factory func(string, string, store.Identity) (store.Store, error)) [2]*lab {
	fixtures := [2]testkit.Fixture{testkit.NewFixture(chain, "org-a", 1), testkit.NewFixture(chain, "org-b", 1)}
	for i := range fixtures {
		fixtures[i].EnableDirect()
	}
	orgs := []protocol.OrgConfig{fixtures[0].Org, fixtures[1].Org}
	gen := fixtures[0].Genesis
	gen.Outputs = append(gen.Outputs, fixtures[1].Genesis.Outputs...)
	gen.Grants = append(gen.Grants, fixtures[1].Genesis.Grants...)
	var keys [2]ed25519.PrivateKey
	var desc [2]protocol.ReceiveDescriptor
	for i, f := range fixtures {
		keys[i] = f.Owner
		desc[i] = protocol.NewDescriptor(f.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: f.Org.Org}, keys[i])
		gen.Wallets = append(gen.Wallets, state.WalletBinding{Owner: desc[i].Owner, Organization: f.Org.Org})
	}
	var labs [2]*lab
	for i := range labs {
		labs[i] = newLabConfigured(t, v, factory, func(l *lab) { l.f = fixtures[i]; l.f.Genesis = gen; l.orgs = orgs; l.keys = keys; l.desc = desc })
	}
	return labs
}

func TestCrossOrganizationUsesFullParentsWithCompactResponses(t *testing.T) {
	labs := newCrossLabs(t, variant{continuation: true, compactReply: true}, func(string, string, store.Identity) (store.Store, error) { return store.NewMemory(), nil })
	var prev *protocol.DirectPayment
	for i := 0; i < 8; i++ {
		sender := labs[i%2]
		receiver := labs[1-i%2]
		req := sender.request(t, i, prev)
		_, compact, mask, e := req.MarshalForContinuation()
		must(t, e)
		if len(compact) != 0 || mask != 0 {
			t.Fatal("cross-organization parent abbreviated")
		}
		cert, e := sender.collector.CollectDirect(context.Background(), req)
		must(t, e)
		must(t, receiver.wallets[1-i%2].ReceiveDirect(req.Tx.Body.Outputs[0], cert, 0))
		prev = &protocol.DirectPayment{Tx: req.Tx, Certificate: cert, InputCertificates: req.InputCertificates}
	}
	if labs[0].misses.Load()+labs[1].misses.Load() != 0 {
		t.Fatal("cross-group path caused reference misses")
	}
}

func TestContinuationEligibilityFollowsSourceNotRecipient(t *testing.T) {
	labs := newCrossLabs(t, variant{continuation: true, compactReply: true}, func(string, string, store.Identity) (store.Store, error) { return store.NewMemory(), nil })
	l := labs[0]
	first := l.request(t, 0, nil)
	body := first.Tx.Body
	body.Outputs[0].Recipient = l.desc[0]
	tx, e := protocol.NewFastTx(body, first.Tx.Claims, l.policy.Key)
	must(t, e)
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), l.keys[0])}
	cert, e := l.collector.CollectDirect(context.Background(), protocol.DirectRequest{Tx: tx})
	must(t, e)
	parent := protocol.DirectPayment{Tx: tx, Certificate: cert}
	// The next receiver belongs to B, but the source and current certifier are A.
	child := l.request(t, 2, &parent)
	if child.Tx.Body.Outputs[0].Recipient.Route.Org == l.f.Org.Org {
		t.Fatal("fixture receiver is not foreign")
	}
	_, compact, mask, e := child.MarshalForContinuation()
	must(t, e)
	if len(compact) == 0 || mask == 0 {
		t.Fatal("foreign destination disabled local-source continuation")
	}
	got, e := l.collector.CollectDirect(context.Background(), child)
	must(t, e)
	must(t, labs[1].wallets[1].ReceiveDirect(child.Tx.Body.Outputs[0], got, 0))
}
