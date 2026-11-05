package main

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/gateway"
	"utxo/internal/member"
	"utxo/internal/recipient"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/transport"
	"utxo/protocol"
)

func TestC3RecipientGatewayCollectRetryAndPrepare(t *testing.T) {
	f := testkit.NewFixture("recipient-c3", "org", 1)
	f.EnableDirect()
	raw, e := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := pem.Decode(raw)
	key, e := x509.ParsePKCS1PrivateKey(b.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	settings := rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	legacy, e := settings.Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	original, e := f.FastTransaction(0, 1, legacy)
	if e != nil {
		t.Fatal(e)
	}
	funding := state.Grant{ID: protocol.Digest("recipient-c3-grant"), Key: protocol.ResourceKey{Kind: protocol.ResourceFUEL, Account: protocol.Digest("recipient-c3-pool"), Version: 1}, Organization: f.Org.Hash(), Amount: 10000, Subject: original.Body.Subject}
	f.Genesis.Grants = append(f.Genesis.Grants, funding)
	id := protocol.Digest("recipient-c3-policy")
	settings.C3 = &rules.C3Settings{Current: id, Policies: []rules.C3Policy{{ID: id, Schedule: protocol.FeeSchedule{AttachReward: 1, ResolveReward: 1, Open: 1, Close: 1, Compensate: 1, Repay: 1}}}, Issuers: []rules.C3Issuer{{Config: f.Org.Hash(), Funding: protocol.AdmissionRef{Key: funding.Key, Grant: funding.ID}, Revenue: protocol.Digest("recipient-c3-revenue"), Beneficiary: protocol.Digest("recipient-c3-reward")}}}
	p, e := settings.Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	body := original.Body
	var refs []protocol.AdmissionRef
	for _, g := range f.Genesis.Grants {
		refs = append(refs, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
	}
	if e = rules.BindC3(&body, p, refs); e != nil {
		t.Fatal(e)
	}
	tx, e := protocol.NewFastTx(body, original.Claims, p.Key)
	if e != nil {
		t.Fatal(e)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.Owner)}
	db, wdb := store.NewMemory(), store.NewMemory()
	defer db.Close()
	defer wdb.Close()
	n := cfg.Network{Genesis: f.Genesis, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Direct: &settings}
	w, e := newRecipientWallet(n, wdb, body.Subject)
	if e != nil {
		t.Fatal(e)
	}
	var clients [4]gateway.MemberClient
	for i := range clients {
		mdb := store.NewMemory()
		defer mdb.Close()
		m, e := member.New(member.Config{Organization: f.Org, Index: uint16(i), Key: f.Keys[i], Peers: n.Organizations, Schedule: f.Schedule, Workers: 1, Direct: &settings}, mdb, f.Genesis)
		if e != nil {
			t.Fatal(e)
		}
		server := httptest.NewServer(transport.MemberHandler(m, 8, 8))
		defer server.Close()
		clients[i] = transport.NewMemberClient(server.URL)
	}
	c, e := gateway.New(f.Org, clients, db)
	if e != nil {
		t.Fatal(e)
	}
	h, e := recipient.New(c, w, body.Subject, f.Org, db)
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	req := protocol.DirectRequest{Tx: tx}
	raw, e = req.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	collect := func() protocol.OutputCertificate {
		t.Helper()
		rw := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v3/recipient/collect", bytes.NewReader(raw))
		r.Header.Set("Content-Type", transport.MediaType)
		h.ServeHTTP(rw, r)
		if rw.Code != 200 {
			t.Fatalf("collect: %d %s", rw.Code, rw.Body.String())
		}
		cert, e := protocol.DecodeOutputCertificate(rw.Body.Bytes())
		if e != nil || cert.Verify(f.Org) != nil {
			t.Fatal("invalid receipt", e)
		}
		return cert
	}
	cert := collect()
	if retry := collect(); retry.QC.Fact != cert.QC.Fact {
		t.Fatal("retry changed fact")
	}
	if _, e = h.Payment(tx.ID()); e != nil {
		t.Fatal("durable receipt absent", e)
	}
	body = tx.Body
	body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: cert.Summary.OutputID(0), Evidence: protocol.Hash(cert.QC.Fact)}}
	body.Nonce[0]++
	if e = rules.BindC3(&body, p, refs); e != nil {
		t.Fatal(e)
	}
	child, e := protocol.NewFastTx(body, []protocol.InputClaim{{Output: tx.Body.Outputs[0]}}, p.Key)
	if e != nil {
		t.Fatal(e)
	}
	child.Auth = []protocol.OwnerAuth{protocol.SignOwner(child.ID(), f.Owner)}
	raw, e = (protocol.DirectRequest{Tx: child, InputCertificates: []protocol.InputCertificate{{Certificate: cert}}}).MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	rw := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v3/recipient/prepare", bytes.NewReader(raw))
	r.Header.Set("Content-Type", transport.MediaType)
	h.PrepareHTTP(rw, r)
	if rw.Code != 204 {
		t.Fatalf("prepare: %d %s", rw.Code, rw.Body.String())
	}
}
