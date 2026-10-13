package wallet

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func directBytesFixture(t *testing.T, db store.Store) (*Wallet, protocol.DirectRequest) {
	t.Helper()
	f := testkit.NewFixture("wallet-saved-bytes", "a", 1)
	f.EnableDirect()
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatal("missing test RSA key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	p, err := (rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}).Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.FastTransaction(0, 19, p)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWithGenesis(db, f.Org.Network, f.Genesis.Outputs[0].Output.Recipient.Owner, []protocol.OrgConfig{f.Org}, f.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	return w, protocol.DirectRequest{Tx: tx}
}

func savedDirectBytes(t *testing.T, db store.Store, req protocol.DirectRequest) []byte {
	t.Helper()
	var raw []byte
	id := req.Tx.ID()
	if err := db.View(func(v state.ReadView) error {
		var err error
		raw, err = v.Get(state.Key(state.KeyWalletRequest, id[:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSaveDirectRequestBytesPersistsExactBytes(t *testing.T) {
	for _, backend := range []string{"memory", "bolt"} {
		t.Run(backend, func(t *testing.T) {
			var db store.Store = store.NewMemory()
			if backend == "bolt" {
				var err error
				db, err = store.Open(filepath.Join(t.TempDir(), "wallet.db"), store.Identity{Network: "wallet-saved-bytes", Role: "wallet", Node: "a", Schema: 1})
				if err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() { db.Close() })
			w, req := directBytesFixture(t, db)
			want, err := req.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := w.SaveDirectRequestBytes(req)
			if err != nil || !bytes.Equal(raw, want) {
				t.Fatalf("saved bytes differ from signed request: err=%v", err)
			}
			if !bytes.Equal(savedDirectBytes(t, db, req), raw) {
				t.Fatal("returned bytes differ from persisted request")
			}
			raw[0] ^= 1
			if !bytes.Equal(savedDirectBytes(t, db, req), want) {
				t.Fatal("mutating returned bytes changed persisted request")
			}
			before, err := store.Scan(db, nil, nil, 1000)
			if err != nil {
				t.Fatal(err)
			}
			retry, err := w.SaveDirectRequestBytes(req)
			if err != nil || !bytes.Equal(retry, want) {
				t.Fatalf("exact retry did not return saved bytes: err=%v", err)
			}
			if err := w.SaveDirectRequest(req); err != nil {
				t.Fatal("compatibility retry failed", err)
			}
			after, err := store.Scan(db, nil, nil, 1000)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("exact retry changed wallet state", err)
			}
		})
	}
}

func TestSaveDirectRequestBytesRejectsChangedOpening(t *testing.T) {
	db := store.NewMemory()
	t.Cleanup(func() { db.Close() })
	w, req := directBytesFixture(t, db)
	if _, err := w.SaveDirectRequestBytes(req); err != nil {
		t.Fatal(err)
	}
	before, err := store.Scan(db, nil, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	altered := req
	altered.Tx.Funding = append([]protocol.Funding(nil), req.Tx.Funding...)
	altered.Tx.Funding[0].Opening[0] ^= 1
	if altered.Tx.ID() != req.Tx.ID() {
		t.Fatal("changed opening must retain immutable TxID")
	}
	if raw, err := w.SaveDirectRequestBytes(altered); !errors.Is(err, protocol.ErrAuth) || raw != nil {
		t.Fatalf("changed opening returned raw=%x, err=%v", raw, err)
	}
	after, err := store.Scan(db, nil, nil, 1000)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rejected retry changed wallet state", err)
	}
}

func TestSaveDirectRequestBytesRejectsBadAuthorization(t *testing.T) {
	db := store.NewMemory()
	t.Cleanup(func() { db.Close() })
	w, req := directBytesFixture(t, db)
	before, err := store.Scan(db, nil, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	req.Tx.Auth[0].Signature[0] ^= 1
	if raw, err := w.SaveDirectRequestBytes(req); !errors.Is(err, protocol.ErrAuth) || raw != nil {
		t.Fatalf("bad authorization returned raw=%x, err=%v", raw, err)
	}
	after, err := store.Scan(db, nil, nil, 1000)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("bad authorization changed wallet state", err)
	}
}

type directBytesFailingStore struct {
	store.Store
	err error
}

func (s directBytesFailingStore) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	return s.Store.Update(func(v state.ReadView) ([]state.Change, error) {
		if _, err := fn(v); err != nil {
			return nil, err
		}
		return nil, s.err
	})
}

func TestSaveDirectRequestBytesStoreFailureReturnsNil(t *testing.T) {
	db := store.NewMemory()
	t.Cleanup(func() { db.Close() })
	w, req := directBytesFixture(t, db)
	before, err := store.Scan(db, nil, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("request commit failed")
	w.db = directBytesFailingStore{Store: db, err: failure}
	if raw, err := w.SaveDirectRequestBytes(req); !errors.Is(err, failure) || raw != nil {
		t.Fatalf("store failure returned raw=%x, err=%v", raw, err)
	}
	after, err := store.Scan(db, nil, nil, 1000)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed update changed wallet state", err)
	}
	w.db = db
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if raw, err := w.SaveDirectRequestBytes(req); !errors.Is(err, store.ErrClosed) || raw != nil {
		t.Fatalf("closed store returned raw=%x, err=%v", raw, err)
	}
}

type directBytesGatedStore struct {
	store.Store
	entered chan struct{}
	release chan struct{}
}

func (s directBytesGatedStore) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	return s.Store.Update(func(v state.ReadView) ([]state.Change, error) {
		changes, err := fn(v)
		if err != nil {
			return nil, err
		}
		close(s.entered)
		<-s.release
		return changes, nil
	})
}

func TestSaveDirectRequestBytesWaitsForUpdate(t *testing.T) {
	db := store.NewMemory()
	t.Cleanup(func() { db.Close() })
	w, req := directBytesFixture(t, db)
	gate := directBytesGatedStore{Store: db, entered: make(chan struct{}), release: make(chan struct{})}
	w.db = gate
	var once sync.Once
	unblock := func() { once.Do(func() { close(gate.release) }) }
	t.Cleanup(unblock)
	type result struct {
		raw []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		raw, err := w.SaveDirectRequestBytes(req)
		done <- result{raw: raw, err: err}
	}()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach update commit boundary")
	}
	select {
	case got := <-done:
		t.Fatalf("returned before update completed: raw=%x, err=%v", got.raw, got.err)
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	select {
	case got := <-done:
		if got.err != nil || len(got.raw) == 0 || !bytes.Equal(got.raw, savedDirectBytes(t, db, req)) {
			t.Fatal("completed update did not return persisted bytes", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not return after update completed")
	}
}
