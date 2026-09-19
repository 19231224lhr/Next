package member_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestDirectDurableApprovalAndBackgroundInstall(t *testing.T) {
	f := testkit.NewFixture("direct-member", "org", 1)
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
	settings := rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	p, err := settings.Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.FastTransaction(0, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := f.FastTransaction(0, 2, p)
	if err != nil {
		t.Fatal(err)
	}
	cert := protocol.OutputCertificate{}
	members := make([]*member.Member, 3)
	var dbs []store.Store
	defer func() {
		for _, db := range dbs {
			db.Close()
		}
	}()
	for i := 0; i < 3; i++ {
		path := filepath.Join(t.TempDir(), "member.db")
		identity := store.Identity{Network: "direct-member", Role: "member", Node: "node", Schema: 3}
		db, err := store.Open(path, identity)
		if err != nil {
			t.Fatal(err)
		}
		cfg := member.Config{Organization: f.Org, Index: uint16(i), Key: f.Keys[i], Peers: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Workers: 1, Direct: &settings}
		m, err := member.New(cfg, db, f.Genesis)
		if err != nil {
			t.Fatal(err)
		}
		vote, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx})
		if err != nil {
			t.Fatal(err)
		}
		cert.Summary = vote.Summary
		cert.QC.Fact = vote.Summary.Fact()
		cert.QC.Votes = append(cert.QC.Votes, vote.Vote)
		quota, err := m.Quota(f.Genesis.Grants[0].Key, 0)
		if err != nil || quota.Reserved != 100 {
			t.Fatalf("CAL not reserved: %+v %v", quota, err)
		}
		if _, err = m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: conflict}); err == nil {
			t.Fatal("conflicting vote issued")
		}
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
		db, err = store.Open(path, identity)
		if err != nil {
			t.Fatal(err)
		}
		dbs = append(dbs, db)
		m, err = member.New(cfg, db, f.Genesis)
		if err != nil {
			t.Fatal(err)
		}
		members[i] = m
		if _, err = m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: conflict}); err == nil {
			t.Fatal("restart forgot input lock")
		}
		again, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx})
		if err != nil || again.Vote != vote.Vote {
			t.Fatal("durable vote not replayable")
		}
	}
	if err = cert.Verify(f.Org); err != nil {
		t.Fatal(err)
	}
	body := tx.Body
	body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: cert.Summary.OutputID(0), Evidence: protocol.Hash(cert.QC.Fact)}}
	body.Nonce[0] = 3
	body.Intent = body.IntentID()
	child, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: tx.Body.Outputs[0]}}, p.Key)
	if err != nil {
		t.Fatal(err)
	}
	child.Auth = []protocol.OwnerAuth{protocol.SignOwner(child.ID(), f.Owner)}
	req := protocol.DirectRequest{Tx: child, InputCertificates: []protocol.InputCertificate{{Certificate: cert, Index: 0}}}
	for _, m := range members {
		if _, err = m.ApproveDirect(context.Background(), req); err != nil {
			t.Fatalf("successor waited for INSTALL: %v", err)
		}
		if err = m.InstallDirect(protocol.DirectPayment{Tx: tx, Certificate: cert}); err != nil {
			t.Fatal(err)
		}
	}
}
