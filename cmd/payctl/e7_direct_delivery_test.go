package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
	"utxo/internal/delivery"
	"utxo/internal/rules"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

func TestE7DirectReceiveTriggersContinuationWithoutSenderForward(t *testing.T) {
	f := testkit.NewFixture("delivery", "org", 1)
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
	policy, e := (rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}).Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	tx, e := f.FastTransaction(0, 1, policy)
	if e != nil {
		t.Fatal(e)
	}
	cert, e := f.DirectCertificate(tx, policy)
	if e != nil {
		t.Fatal(e)
	}
	db := store.NewMemory()
	defer db.Close()
	local, e := wallet.NewWithGenesis(db, f.Org.Network, tx.Body.Outputs[0].Recipient.Owner, []protocol.OrgConfig{f.Org}, f.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	w := &e7Wallet{wallets: []*wallet.Wallet{local}, descriptors: []protocol.ReceiveDescriptor{tx.Body.Outputs[0].Recipient}, receipts: newE7Receipts(), ready: newE7Ready(1), active: true, end: time.Now().Add(time.Second), report: &e7Report{Options: e7RunOptions{Phase: "formal", Chain: true}}}
	w.network.Genesis = f.Genesis
	p := protocol.DirectPayment{Tx: tx, Certificate: cert}
	raw, e = p.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	meta, e := (e7DirectMetadata{Phase: "formal", Index: 7, Receiver: 0, Hop: 1, Tx: tx.ID()}).header()
	if e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("POST", "/v3/receive", bytes.NewReader(raw))
	req.Header.Set("Content-Type", transport.MediaType)
	req.Header.Set("X-Next-Output-Index", "0")
	req.Header.Set(delivery.MetadataHeader, meta)
	rw := httptest.NewRecorder()
	h := w.directReceiver()
	h.ServeHTTP(rw, req)
	if rw.Code != 200 {
		t.Fatalf("receive %d %s", rw.Code, rw.Body.String())
	}
	task, ok := w.ready.take()
	if !ok || task.Hop != 2 || task.Input != cert.Summary.OutputID(0) {
		t.Fatalf("continuation missing: %+v", task)
	}
	req = httptest.NewRequest("POST", "/v3/receive", bytes.NewReader(raw))
	req.Header.Set("Content-Type", transport.MediaType)
	req.Header.Set("X-Next-Output-Index", "0")
	req.Header.Set(delivery.MetadataHeader, meta)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if _, ok = w.ready.take(); ok {
		t.Fatal("duplicate continuation")
	}
}

func TestE7DirectMetadataAttachedAndAcknowledgementBound(t *testing.T) {
	meta := e7DirectMetadata{Phase: "formal", Index: 7, Receiver: 1, Hop: 2, Tx: protocol.TxID{1}}
	h, e := meta.header()
	if e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(delivery.MetadataHeader) != h {
			t.Error("metadata not propagated")
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := e7DeliveryClient(srv.Client(), h)
	resp, e := c.Get(srv.URL)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	receipts := newE7Receipts()
	receipts.accept(7, func() (e7Ack, error) { return e7Ack{Index: 7, Fact: protocol.SpendFactID{2}}, nil })
	w := &e7Wallet{receipts: receipts}
	server := httptest.NewServer(http.HandlerFunc(w.directAck))
	defer server.Close()
	if _, e = e7WaitDirectAck(context.Background(), server.Client(), server.URL, 7, protocol.SpendFactID{3}); e == nil {
		t.Fatal("wrong fact ack accepted")
	}
	a, e := e7WaitDirectAck(context.Background(), server.Client(), server.URL, 7, protocol.SpendFactID{2})
	if e != nil || a.Index != 7 {
		t.Fatalf("ack %v %v", a, e)
	}
}
