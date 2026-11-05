package gateway

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestGroupedDirectOutboxDoesNotReviveAfterBlock(t *testing.T) {
	f := testkit.NewFixture("grouped-outbox", "org", 1)
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
	payment := protocol.DirectPayment{Tx: tx, Certificate: cert}
	raw, err = payment.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	data, err := (protocol.ExecutionResult{Applied: true}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	public, err := payment.Submission().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	trust, proof := testkit.Block("grouped-outbox", 1, nil, [][]byte{public}, []*abci.ExecTxResult{{Data: data}})
	verified, err := finality.VerifyBlock(trust, proof)
	if err != nil {
		t.Fatal(err)
	}
	fact := cert.QC.Fact
	outbox := state.Key(state.KeyOutbox, fact[:])
	pending := state.Outbox{Fact: fact, Certificate: raw, Origin: f.Org.Org}
	for _, order := range []string{"retry-first", "follow-first", "follow-before-save"} {
		t.Run(order, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gateway.db")
			identity := store.Identity{Network: "test", Role: "gateway", Node: "org", Schema: 4}
			base, err := store.Open(path, identity)
			if err != nil {
				t.Fatal(err)
			}
			db, err := store.NewGroup(base, 256, 64)
			if err != nil {
				base.Close()
				t.Fatal(err)
			}
			defer db.Close()
			var clients [4]MemberClient
			for i := range clients {
				clients[i] = new(countedInstaller)
			}
			collector, err := New(f.Org, clients, db)
			if err != nil {
				t.Fatal(err)
			}
			r := Relay{DB: db, Public: new(retryPublic), MemberRelay: true, Organizations: map[protocol.Hash]protocol.OrgConfig{f.Org.Hash(): f.Org}}
			save := func() {
				t.Helper()
				if err := collector.PersistDirect(payment); err != nil {
					t.Fatal(err)
				}
			}
			follow := func() {
				t.Helper()
				if err := blockfollow.Commit(db, verified, r.ObserveBlock(f.Org.Hash())); err != nil {
					t.Fatal(err)
				}
			}
			retry := func() {
				t.Helper()
				if err := r.deliverDirect(context.Background(), outbox, pending); err != nil {
					t.Fatal(err)
				}
			}
			switch order {
			case "retry-first":
				save()
				retry()
				follow()
			case "follow-first":
				save()
				follow()
				retry()
			case "follow-before-save":
				follow()
				save()
				retry()
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(path, identity)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if err := reopened.View(func(v state.ReadView) error {
				if _, err := v.Get(outbox); !errors.Is(err, state.ErrNotFound) {
					return errors.New("completed outbox revived")
				}
				observed, found, err := state.Load[bool](v, state.Key(state.KeyObserved, fact[:]))
				if err != nil {
					return err
				}
				if !found || !observed {
					return errors.New("lost completion marker")
				}
				id := tx.ID()
				_, err = v.Get(state.Key(state.KeyCollected, id[:]))
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
