package member_test

import (
	"context"
	"reflect"
	"testing"
	"utxo/internal/blockfollow"
	"utxo/internal/member"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/wallet"
	"utxo/protocol"
)

func TestDirectRetryChecksCurrentWalletBinding(t *testing.T) {
	x := newReclaimLab(t, 300)
	tx := x.tx(t, 0, 29, false)
	x.approve(t, tx, 0)
	if err := x.db[0].Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if err := state.Put(o, state.Key(state.KeyWalletBinding, tx.Body.Outputs[0].Recipient.Owner[:]), protocol.Digest("corrupted fixed binding")); err != nil {
			return nil, err
		}
		return o.Changes(), nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := store.Scan(x.db[0], nil, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.m[0].ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx}); err == nil {
		t.Fatal("approval retry bypassed current wallet binding")
	}
	after, err := store.Scan(x.db[0], nil, nil, 1000)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("route rejection changed member state", err)
	}
}

func TestMemberReopenRequiresAllGenesisOrganizations(t *testing.T) {
	x := newReclaimLab(t, 300)
	other := testkit.NewFixture(x.identity.Network, "other", 1)
	other.Org.Network = x.f.Org.Network
	other.Genesis.Network = x.f.Org.Network
	other.Genesis.Outputs[0].Output.Recipient = protocol.NewDescriptor(x.f.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: other.Org.Org}, other.Owner)
	genesis := x.f.Genesis
	genesis.Outputs = append(append([]state.OriginOutput(nil), genesis.Outputs...), other.Genesis.Outputs...)
	cfg := x.cfg[0]
	cfg.Peers = append(cfg.Peers, other.Org)
	db := store.NewMemory()
	t.Cleanup(func() { db.Close() })
	if _, err := member.New(cfg, db, genesis); err != nil {
		t.Fatal(err)
	}
	cfg.Peers = []protocol.OrgConfig{x.f.Org}
	if _, err := member.New(cfg, db, genesis); err == nil {
		t.Fatal("reopen ignored unknown genesis wallet organization")
	}
}

func TestDirectRejectsRecipientWrongOrganizationAtomically(t *testing.T) {
	x := newReclaimLab(t, 300)
	tx := x.tx(t, 0, 17, false)
	body := tx.Body
	body.Outputs = append([]protocol.Output(nil), body.Outputs...)
	body.Outputs[0].Recipient = protocol.NewDescriptor(x.f.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: protocol.Digest("other organization")}, x.f.Owner)
	tx, err := protocol.NewFastTx(body, tx.Claims, x.p.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), x.f.Owner)}
	before, err := store.Scan(x.db[0], nil, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.m[0].ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx}); err == nil {
		t.Fatal("owner output accepted under wrong organization")
	}
	after, err := store.Scan(x.db[0], nil, nil, 1000)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("route rejection leaked input locks or debits", err)
	}
}

func TestDirectIndependentInputsReuseSubjectNonce(t *testing.T) {
	x := newReclaimLab(t, 300)
	a, b := x.tx(t, 0, 7, false), x.tx(t, 1, 7, false)
	if a.ID() == b.ID() || a.Body.Subject != b.Body.Subject || a.Body.Nonce != b.Body.Nonce {
		t.Fatal("fixture must have distinct transaction identities and equal subject/nonce")
	}
	first, second := x.approve(t, a, 0, 1, 2), x.approve(t, b, 0, 1, 2)
	if err := first.Certificate.Verify(x.f.Org); err != nil {
		t.Fatal(err)
	}
	if err := second.Certificate.Verify(x.f.Org); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		before, err := store.Scan(x.db[i], nil, nil, 1000)
		if err != nil {
			t.Fatal(err)
		}
		for _, tx := range []protocol.FastTx{a, b} {
			if _, err := x.m[i].ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx}); err != nil {
				t.Fatal(err)
			}
		}
		after, err := store.Scan(x.db[i], nil, nil, 1000)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("retry changed locks or reserved budget", err)
		}
	}
	block := x.block(t, first, second)
	for i := 0; i < 3; i++ {
		if err := blockfollow.Commit(x.db[i], block, x.m[i].PrepareBlock); err != nil {
			t.Fatal(err)
		}
		quota, err := x.m[i].Quota(x.f.Genesis.Grants[0].Key, 0)
		if err != nil || quota.Reserved != 0 || quota.Available != 200 {
			t.Fatal("independent settlements did not restore both reservations", quota, err)
		}
	}
}

func TestWalletIndependentInputsReuseSubjectNonce(t *testing.T) {
	x := newReclaimLab(t, 300)
	db := store.NewMemory()
	t.Cleanup(func() { db.Close() })
	w, err := wallet.NewWithGenesis(db, x.f.Org.Network, x.f.Genesis.Outputs[0].Output.Recipient.Owner, []protocol.OrgConfig{x.f.Org}, x.f.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	a, b := x.tx(t, 0, 7, false), x.tx(t, 1, 7, false)
	for _, tx := range []protocol.FastTx{a, b} {
		if err := w.SaveDirectRequest(protocol.DirectRequest{Tx: tx}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.Scan(db, nil, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, tx := range []protocol.FastTx{a, b} {
		if err := w.SaveDirectRequest(protocol.DirectRequest{Tx: tx}); err != nil {
			t.Fatal(err)
		}
	}
	after, err := store.Scan(db, nil, nil, 1000)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("retry changed request journal", err)
	}
	if err := w.SaveDirectRequest(protocol.DirectRequest{Tx: x.tx(t, 0, 8, false)}); err == nil {
		t.Fatal("shared input reserved for second transaction")
	}
}

func TestWalletDirectRequiresGenesisBinding(t *testing.T) {
	x := newReclaimLab(t, 300)
	db := store.NewMemory()
	t.Cleanup(func() { db.Close() })
	w, err := wallet.New(db, x.f.Org.Network, x.f.Genesis.Outputs[0].Output.Recipient.Owner, []protocol.OrgConfig{x.f.Org})
	if err != nil {
		t.Fatal(err)
	}
	tx := x.tx(t, 0, 18, false)
	if err := w.SaveDirectRequest(protocol.DirectRequest{Tx: tx}); err == nil {
		t.Fatal("unbound wallet saved direct request")
	}
	c, err := x.f.DirectCertificate(tx, x.p)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ReceiveDirect(tx.Body.Outputs[0], c, 0); err == nil {
		t.Fatal("unbound wallet received direct output")
	}
}

func TestWalletRetryRequiresExactRequest(t *testing.T) {
	x := newReclaimLab(t, 300)
	db := store.NewMemory()
	t.Cleanup(func() { db.Close() })
	w, err := wallet.NewWithGenesis(db, x.f.Org.Network, x.f.Genesis.Outputs[0].Output.Recipient.Owner, []protocol.OrgConfig{x.f.Org}, x.f.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	tx := x.tx(t, 0, 19, false)
	if err := w.SaveDirectRequest(protocol.DirectRequest{Tx: tx}); err != nil {
		t.Fatal(err)
	}
	before, err := store.Scan(db, nil, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	altered := tx
	altered.Funding = append([]protocol.Funding(nil), tx.Funding...)
	altered.Funding[0].Opening[0] ^= 1
	if tx.ID() != altered.ID() {
		t.Fatal("opening changed immutable TxID")
	}
	if err := w.SaveDirectRequest(protocol.DirectRequest{Tx: altered}); err == nil {
		t.Fatal("same TxID accepted changed request bytes")
	}
	after, err := store.Scan(db, nil, nil, 1000)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rejected retry changed wallet state", err)
	}
}

func TestWalletRejectsWrongOrganization(t *testing.T) {
	x := newReclaimLab(t, 300)
	db := store.NewMemory()
	t.Cleanup(func() { db.Close() })
	w, err := wallet.NewWithGenesis(db, x.f.Org.Network, x.f.Genesis.Outputs[0].Output.Recipient.Owner, []protocol.OrgConfig{x.f.Org}, x.f.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	tx := x.tx(t, 0, 37, false)
	body := tx.Body
	body.Outputs = append([]protocol.Output(nil), body.Outputs...)
	body.Outputs[0].Recipient = protocol.NewDescriptor(x.f.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: protocol.Digest("wrong wallet organization")}, x.f.Owner)
	tx, err = protocol.NewFastTx(body, tx.Claims, x.p.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), x.f.Owner)}
	certificate, err := x.f.DirectCertificate(tx, x.p)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Scan(db, nil, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SaveDirectRequest(protocol.DirectRequest{Tx: tx}); err == nil {
		t.Fatal("wrong organization request saved")
	}
	if err := w.ReceiveDirect(tx.Body.Outputs[0], certificate, 0); err == nil {
		t.Fatal("wrong organization certificate received")
	}
	after, err := store.Scan(db, nil, nil, 1000)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("route rejection changed wallet state", err)
	}
}
