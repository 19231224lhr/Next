package main

import (
	"context"
	"crypto/sha256"
	"testing"

	"utxo/protocol"
)

func TestE5GateRequiresThreeDistinctStoredCopiesOrVerifiedPublicSuccess(t *testing.T) {
	gate := newE5Gate(1)
	key := e5RequestKey{Network: protocol.Digest("network", []byte("n")), Tx: protocol.TxID(protocol.Digest("tx", []byte("1")))}
	raw := []byte("signed request")
	digest := sha256.Sum256(raw)
	fact := protocol.SpendFactID(protocol.Digest("fact", []byte("1")))
	if _, err := gate.Register(key, digest); err != nil {
		t.Fatal(err)
	}
	if err := gate.Stored(key, digest, fact, 0); err != nil {
		t.Fatal(err)
	}
	if err := gate.Stored(key, digest, fact, 0); err != nil {
		t.Fatal(err)
	}
	if ready, _ := gate.Ready(key); ready {
		t.Fatal("one member counted twice")
	}
	if err := gate.Stored(key, digest, fact, 1); err != nil {
		t.Fatal(err)
	}
	if ready, _ := gate.Ready(key); ready {
		t.Fatal("two copies satisfied three-member gate")
	}
	if err := gate.Stored(key, digest, fact, 2); err != nil {
		t.Fatal(err)
	}
	if ready, reason := gate.Ready(key); !ready || reason != "install3" {
		t.Fatalf("three distinct stored copies: ready=%v reason=%q", ready, reason)
	}
	if _, err := gate.Register(key, sha256.Sum256([]byte("different request"))); err == nil {
		t.Fatal("same Network/TxID merged different signed request")
	}
	if _, err := gate.Register(e5RequestKey{Network: key.Network, Tx: protocol.TxID(protocol.Digest("tx", []byte("2")))}, digest); err == nil {
		t.Fatal("gate capacity was not enforced")
	}
}

func TestE5GateVerifiedPublicSuccessSatisfiesBeforeCopies(t *testing.T) {
	gate := newE5Gate(1)
	key := e5RequestKey{Network: protocol.Digest("network", []byte("n")), Tx: protocol.TxID(protocol.Digest("tx", []byte("1")))}
	digest := sha256.Sum256([]byte("signed request"))
	fact := protocol.SpendFactID(protocol.Digest("fact", []byte("1")))
	if _, err := gate.Register(key, digest); err != nil {
		t.Fatal(err)
	}
	if err := gate.Public(key, digest, fact); err != nil {
		t.Fatal(err)
	}
	if ready, reason := gate.Ready(key); !ready || reason != "public" {
		t.Fatalf("public success gate: ready=%v reason=%q", ready, reason)
	}
	other := protocol.SpendFactID(protocol.Digest("fact", []byte("other")))
	if err := gate.Stored(key, digest, other, 0); err == nil {
		t.Fatal("conflicting fact was accepted for one request")
	}
}

func TestE5GateWaitCancellationDoesNotDeleteLogicalRequest(t *testing.T) {
	gate := newE5Gate(1)
	key := e5RequestKey{Network: protocol.Digest("network", []byte("n")), Tx: protocol.TxID(protocol.Digest("tx", []byte("1")))}
	digest := sha256.Sum256([]byte("signed request"))
	if _, err := gate.Register(key, digest); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gate.Wait(ctx, key); err == nil {
		t.Fatal("cancelled wait returned success")
	}
	fact := protocol.SpendFactID(protocol.Digest("fact", []byte("1")))
	for i := uint16(0); i < 3; i++ {
		if err := gate.Stored(key, digest, fact, i); err != nil {
			t.Fatal(err)
		}
	}
	if reason, err := gate.Wait(context.Background(), key); err != nil || reason != "install3" {
		t.Fatalf("logical request lost after HTTP cancellation: reason=%q err=%v", reason, err)
	}
}
