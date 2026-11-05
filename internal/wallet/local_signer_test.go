package wallet

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func localTestKey() ed25519.PrivateKey {
	seed := protocol.Digest("LAB_OWNER", []byte("wallet-saved-bytes"), []byte("a"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func TestLocalSignAndSaveEquivalent(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	w, req := directBytesFixture(t, db)
	want, err := req.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	key := localTestKey()
	signer, err := w.NewLocalDirectSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	key[0] ^= 1 // signer must own and validate the key at construction.
	req.Tx.Auth = nil
	tx, id, raw, err := signer.SignAndSave(req)
	if err != nil || id != tx.ID() || !bytes.Equal(want, raw) {
		t.Fatalf("not equivalent: %v", err)
	}
	decoded, err := protocol.DecodeDirectRequest(raw)
	if err != nil || decoded.Tx.VerifyAuth() != nil {
		t.Fatal("invalid public request", err)
	}
	if !bytes.Equal(raw, savedDirectBytes(t, db, protocol.DirectRequest{Tx: tx})) {
		t.Fatal("journal mismatch")
	}
	before, _ := store.Scan(db, nil, nil, 1000)
	_, _, retry, err := signer.SignAndSave(req)
	after, _ := store.Scan(db, nil, nil, 1000)
	if err != nil || !bytes.Equal(raw, retry) || !reflect.DeepEqual(before, after) {
		t.Fatal("retry changed state", err)
	}
	req.Tx.Funding[0].Opening[0] ^= 1
	if _, _, raw, err := signer.SignAndSave(req); err == nil || raw != nil {
		t.Fatal("same ID/different opening accepted")
	}
	if bytes.Equal(tx.Funding[0].Opening[:], req.Tx.Funding[0].Opening[:]) {
		t.Fatal("returned transaction aliases caller")
	}
}

func TestLocalSignAndSaveRejects(t *testing.T) {
	for _, name := range []string{"signed", "owner", "claim", "funding", "length", "version", "invalid-c3", "store"} {
		t.Run(name, func(t *testing.T) {
			db := store.NewMemory()
			defer db.Close()
			w, req := directBytesFixture(t, db)
			signer, err := w.NewLocalDirectSigner(localTestKey())
			if err != nil {
				t.Fatal(err)
			}
			if name != "signed" {
				req.Tx.Auth = nil
			}
			switch name {
			case "owner":
				req.Tx.Body.Subject[0] ^= 1
			case "claim":
				req.Tx.Claims[0].Output.Recipient.Owner[0] ^= 1
			case "funding":
				req.Tx.Funding[0].Kind = 255
			case "length":
				req.Tx.Claims = nil
			case "version":
				req.Tx.Body.Version = 999
			case "invalid-c3":
				req.Tx.Body.Fee.C3.Quote = 1
				req.Tx.Body.Fee.Source = protocol.OwnerFinalUTXO
			case "store":
				w.db = directBytesFailingStore{Store: db, err: errors.New("commit failed")}
			}
			before, _ := store.Scan(db, nil, nil, 1000)
			_, id, raw, err := signer.SignAndSave(req)
			after, _ := store.Scan(db, nil, nil, 1000)
			if err == nil || raw != nil || id != (protocol.TxID{}) || !reflect.DeepEqual(before, after) {
				t.Fatalf("accepted/changed state: %v", err)
			}
		})
	}
}

func TestLocalSignerRejectsMalformedKeys(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	w, _ := directBytesFixture(t, db)
	for _, key := range []ed25519.PrivateKey{nil, make([]byte, 32), make([]byte, 64)} {
		if _, err := w.NewLocalDirectSigner(key); err == nil {
			t.Fatal("malformed key accepted")
		}
	}
	key := localTestKey()
	key[63] ^= 1
	if _, err := w.NewLocalDirectSigner(key); err == nil {
		t.Fatal("mismatched public half accepted")
	}
	key = ed25519.NewKeyFromSeed(make([]byte, 32))
	if _, err := w.NewLocalDirectSigner(key); err == nil {
		t.Fatal("wrong wallet owner accepted")
	}
}

func TestLocalSignAndSaveWaitsAndOwnsSnapshot(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	w, req := directBytesFixture(t, db)
	want, _ := req.MarshalBinary()
	req.Tx.Auth = nil
	signer, err := w.NewLocalDirectSigner(localTestKey())
	if err != nil {
		t.Fatal(err)
	}
	gate := directBytesGatedStore{Store: db, entered: make(chan struct{}), release: make(chan struct{})}
	w.db = gate
	var once sync.Once
	release := func() { once.Do(func() { close(gate.release) }) }
	defer release()
	type result struct {
		tx  protocol.FastTx
		raw []byte
		err error
	}
	done := make(chan result, 1)
	go func() { tx, _, raw, e := signer.SignAndSave(req); done <- result{tx, raw, e} }()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("did not reach commit")
	}
	req.Tx.Body.Outputs[0].Amount++
	req.Tx.Funding[0].Opening[0] ^= 1
	select {
	case <-done:
		t.Fatal("returned before commit")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case got := <-done:
		if got.err != nil || !bytes.Equal(got.raw, want) || !bytes.Equal(savedDirectBytes(t, db, protocol.DirectRequest{Tx: got.tx}), want) {
			t.Fatal("snapshot/commit mismatch", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not finish")
	}
}

func TestLocalSignAndSaveChecksCurrentState(t *testing.T) {
	for _, mode := range []string{"binding", "recovered", "spent"} {
		t.Run(mode, func(t *testing.T) {
			db := store.NewMemory()
			defer db.Close()
			w, req := directBytesFixture(t, db)
			req.Tx.Auth = nil
			signer, err := w.NewLocalDirectSigner(localTestKey())
			if err != nil {
				t.Fatal(err)
			}
			in := req.Tx.Body.Inputs[0]
			err = db.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				switch mode {
				case "binding":
					o.Delete(state.Key(state.KeyWalletBinding, w.owner[:]))
				case "recovered":
					if e := state.Put(o, DirectCoinKey(in.Output, 0), DirectCoin{Output: req.Tx.Claims[0].Output, Recovered: true}); e != nil {
						return nil, e
					}
				case "spent":
					if e := state.Put(o, state.Key(state.KeyWalletSpend, in.Output[:], []byte{0}), protocol.TxID{99}); e != nil {
						return nil, e
					}
				}
				return o.Changes(), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			before, _ := store.Scan(db, nil, nil, 1000)
			_, _, raw, err := signer.SignAndSave(req)
			after, _ := store.Scan(db, nil, nil, 1000)
			if err == nil || raw != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("stale state accepted", err)
			}
		})
	}
}
