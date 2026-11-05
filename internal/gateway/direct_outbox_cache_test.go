package gateway

import (
	"bytes"
	"encoding/json"
	"testing"

	"utxo/internal/state"
)

func TestDirectOutboxCache(t *testing.T) {
	raw, _ := json.Marshal(state.Outbox{Certificate: []byte("evidence"), Attempt: []byte("attempt")})
	c := newDirectOutboxCache(2, 4096)
	p, err := c.decode([]byte("a"), raw)
	if err != nil {
		t.Fatal(err)
	}
	first := c.entries["a"]
	p.Certificate[0] = 'X'
	p.Attempt[0] = 'X'
	again, err := c.decode([]byte("a"), bytes.Clone(raw))
	if err != nil || string(again.Certificate) != "evidence" || string(again.Attempt) != "attempt" || c.entries["a"] != first {
		t.Fatal("unchanged bytes not reused safely", err)
	}
	raw[0] = '!'
	if _, err := c.decode([]byte("a"), raw); err == nil {
		t.Fatal("malformed changed state accepted")
	}
	if len(c.entries) != 0 {
		t.Fatal("malformed replacement retained old entry")
	}
	changed, _ := json.Marshal(state.Outbox{PublicComplete: true, NextSubmitUnixNS: 99})
	p, err = c.decode([]byte("a"), changed)
	if err != nil || !p.PublicComplete || p.NextSubmitUnixNS != 99 {
		t.Fatal("changed durable state not decoded", err)
	}
	c.decode([]byte("b"), changed)
	c.decode([]byte("a"), changed) // Recently used entries survive eviction.
	c.decode([]byte("c"), changed)
	if len(c.entries) != 2 || c.entries["b"] != nil || c.entries["a"] == nil {
		t.Fatal("entry limit not enforced")
	}
	if _, err := c.decode([]byte("a"), []byte(`{"Certificate":123}`)); err == nil {
		t.Fatal("invalid field type accepted")
	}
}

func TestDirectOutboxCacheByteBoundAndInputOwnership(t *testing.T) {
	raw, _ := json.Marshal(state.Outbox{Certificate: []byte("evidence")})
	c := newDirectOutboxCache(10, 2*(len(raw)+len("evidence")+1)-1)
	key := []byte("a")
	c.decode(key, raw)
	key[0] = 'z'
	original := bytes.Clone(raw)
	raw[0] = '!'
	if _, err := c.decode([]byte("a"), original); err != nil {
		t.Fatal(err)
	}
	c.decode([]byte("b"), original)
	if len(c.entries) != 1 || c.bytes > c.maxBytes {
		t.Fatal("byte limit not enforced")
	}
	c = newDirectOutboxCache(10, 1)
	if _, err := c.decode([]byte("a"), original); err != nil || len(c.entries) != 0 {
		t.Fatal("oversized entry should decode without caching", err)
	}
}

func TestDirectOutboxSnapshotRetainsOwnedDataAfterReplacementAndEviction(t *testing.T) {
	raw, _ := json.Marshal(state.Outbox{Certificate: []byte("evidence"), Attempt: []byte("attempt")})
	c := newDirectOutboxCache(1, 4096)
	first, err := c.snapshot([]byte("a"), raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.snapshot([]byte("a"), bytes.Clone(raw))
	if err != nil || &first.Certificate[0] != &second.Certificate[0] {
		t.Fatal("snapshot hit cloned evidence", err)
	}
	raw[0] = '!'
	if string(first.Certificate) != "evidence" {
		t.Fatal("input mutation changed snapshot")
	}
	changed, _ := json.Marshal(state.Outbox{Certificate: []byte("replacement")})
	c.snapshot([]byte("a"), changed)
	c.snapshot([]byte("b"), changed)
	if string(first.Certificate) != "evidence" || string(first.Attempt) != "attempt" {
		t.Fatal("replacement or eviction mutated captured snapshot")
	}
	if _, err := c.snapshot([]byte("b"), []byte(`{"Certificate":123}`)); err == nil || len(c.entries) != 0 {
		t.Fatal("malformed replacement retained snapshot")
	}
}
