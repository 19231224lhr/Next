package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"utxo/internal/gateway"
	"utxo/internal/rules"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/transport"
	"utxo/protocol"
)

type readyDirectMember struct {
	gateway.MemberClient
	approval protocol.DirectApproval
}

func (m readyDirectMember) ApproveDirect(context.Context, protocol.DirectRequest) (protocol.DirectApproval, error) {
	return m.approval, nil
}
func (m readyDirectMember) InstallDirect(context.Context, protocol.DirectPayment) error { return nil }

func TestDirectHTTPResponseAndEarlyOfferBeforePersistence(t *testing.T) {
	f := testkit.NewFixture("early-http", "org", 1)
	f.EnableDirect()
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := (rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}).Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.FastTransaction(0, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := f.DirectCertificate(tx, policy)
	if err != nil {
		t.Fatal(err)
	}
	var clients [4]gateway.MemberClient
	for i := range clients {
		clients[i] = readyDirectMember{approval: protocol.DirectApproval{Summary: cert.Summary, Vote: protocol.SignSpend(cert.QC.Fact, uint16(i), f.Keys[i])}}
	}
	db := &blockedStore{Store: store.NewMemory(), entered: make(chan struct{}), release: make(chan struct{})}
	defer db.Close()
	c, err := gateway.New(f.Org, clients, db)
	if err != nil {
		t.Fatal(err)
	}
	offered := make(chan protocol.DirectPayment, 1)
	c.OfferDirect = func(p protocol.DirectPayment) bool { offered <- p; return true }
	srv := httptest.NewServer(directPaymentHandler(c))
	defer srv.Close()
	defer close(db.release)
	raw, err = (protocol.DirectRequest{Tx: tx}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(srv.URL, transport.MediaType, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	got, err := protocol.DecodeOutputCertificate(body)
	if err != nil || got.Verify(f.Org) != nil {
		t.Fatal("invalid response", err)
	}
	select {
	case p := <-offered:
		if p.Tx.ID() != tx.ID() || p.Certificate.Verify(f.Org) != nil {
			t.Fatal("wrong early payload")
		}
	case <-time.After(time.Second):
		t.Fatal("early offer waited for persistence")
	}
	select {
	case <-db.entered:
	case <-time.After(time.Second):
		t.Fatal("durable save was skipped")
	}
}
