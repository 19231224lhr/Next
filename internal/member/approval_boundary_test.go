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
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

// Simulate interruption after the real synchronous transaction commits but
// before Update returns to the signer. No production state is rolled back.
type committedApprovalStop struct {
	store.Store
	armed bool
}

func (s *committedApprovalStop) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	err := s.Store.Update(fn)
	if err == nil && s.armed {
		panic("approval committed before signature return")
	}
	return err
}

func TestApprovalCommittedBeforeSignatureReturn(t *testing.T) {
	f := testkit.NewFixture("approval-boundary", "org", 1)
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
	policy, err := settings.Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.FastTransaction(0, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := f.FastTransaction(0, 2, policy)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "member.db")
	identity := store.Identity{Network: "approval-boundary", Role: "member", Node: "0", Schema: 3}
	db, err := store.Open(path, identity)
	if err != nil {
		t.Fatal(err)
	}
	gate := &committedApprovalStop{Store: db}
	cfg := member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Peers: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Workers: 1, Direct: &settings}
	m, err := member.New(cfg, gate, f.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	gate.armed = true
	stopped := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				if r != "approval committed before signature return" {
					panic(r)
				}
				stopped = true
			}
		}()
		_, _ = m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx})
	}()
	if !stopped {
		t.Fatal("boundary did not interrupt the signer")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err = member.New(cfg, db, f.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	before, err := m.Quota(f.Genesis.Grants[0].Key, 0)
	if err != nil || before.Reserved != 100 {
		t.Fatalf("committed debit lost: %+v %v", before, err)
	}
	if _, err = m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: conflict}); err == nil {
		t.Fatal("committed lock lost")
	}
	vote, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx})
	if err != nil {
		t.Fatal(err)
	}
	if vote.Vote != protocol.SignSpend(vote.Summary.Fact(), 0, f.Keys[0]) {
		t.Fatal("retry signed different fact")
	}
	after, err := m.Quota(f.Genesis.Grants[0].Key, 0)
	if err != nil || before != after {
		t.Fatalf("retry reserved twice: before=%+v after=%+v err=%v", before, after, err)
	}
}
