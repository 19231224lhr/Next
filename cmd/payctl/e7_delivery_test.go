package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/rules"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/wallet"
	"utxo/protocol"
)

func TestE7ReceiverVerifiesAndQueuesBeforeACK(t *testing.T) {
	f := testkit.NewFixture("e7-receive", "org", 1)
	f.EnableDirect()
	raw, e := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if e != nil {
		t.Fatal(e)
	}
	block, _ := pem.Decode(raw)
	key, e := x509.ParsePKCS1PrivateKey(block.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	p, e := (rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}).Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	tx, e := f.FastTransaction(0, 1, p)
	if e != nil {
		t.Fatal(e)
	}
	cert, e := f.DirectCertificate(tx, p)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := cert.MarshalBinary()
	db := store.NewMemory()
	defer db.Close()
	receiver, e := wallet.New(db, tx.Body.Network, tx.Body.Outputs[0].Recipient.Owner, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	w := &e7Wallet{config: e7WalletConfig{Site: 0}, wallets: make([]*wallet.Wallet, 64), receipts: newE7Receipts(), report: &e7Report{Options: e7RunOptions{Phase: "formal", Chain: true}}, active: true, end: time.Now().Add(time.Minute), ready: newE7Ready(64), roots: map[[2]int]time.Time{}}
	w.network = cfg.Network{}
	w.network.Genesis.Network = tx.Body.Network
	w.wallets[0] = receiver
	server := httptest.NewServer(http.HandlerFunc(w.receive))
	defer server.Close()
	d := e7Delivery{Phase: "formal", Index: 1, Lane: 1, Hop: 1, Receiver: 0, Output: tx.Body.Outputs[0], Certificate: encoded}
	bad := d
	bad.Output.Amount++
	if _, e = e7SendReceipt(context.Background(), server.Client(), server.URL, bad); e == nil {
		t.Fatal("unbound output accepted")
	}
	if w.ready.len() != 0 {
		t.Fatal("invalid receipt triggered next hop")
	}
	a, e := e7SendReceipt(context.Background(), server.Client(), server.URL, d)
	if e != nil || a.Fact != cert.QC.Fact {
		t.Fatalf("ack: %v %v", a, e)
	}
	next, ok := w.ready.take()
	if !ok || next.Parent != 1 || next.Hop != 2 || next.Input != protocol.OutputIdentity(tx.Body.Network, tx.ID(), 0) {
		t.Fatal("next payment not triggered by receive")
	}
	if _, e = e7SendReceipt(context.Background(), server.Client(), server.URL, d); e != nil {
		t.Fatal(e)
	}
	if w.ready.len() != 0 {
		t.Fatal("duplicate continuation")
	}
	w.end = time.Now().Add(-time.Second)
	d.Index = 2
	d.Lane = 2
	if _, e = e7SendReceipt(context.Background(), server.Client(), server.URL, d); e != nil {
		t.Fatal(e)
	}
	if w.ready.len() != 0 {
		t.Fatal("continuation after cutoff")
	}
}
