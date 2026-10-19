package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestDirectCoinStatusCacheConcurrentReplacement(t *testing.T) {
	var c directCoinStatusCache
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 512; i++ {
				key := []byte(fmt.Sprintf("coin-%d", i%300))
				raw := []byte(fmt.Sprintf("version-%d-%d", worker, i))
				want := directCoinStatus{Final: protocol.Hash{byte(worker), byte(i)}, Recovered: i%2 == 0}
				c.remember(key, raw, want)
				// Replacement or eviction may cause a miss, but a hit must
				// always carry the status belonging to these exact bytes.
				if got, hit := c.lookup(key, raw); hit && got != want {
					t.Errorf("status from a different record: got %+v, want %+v", got, want)
				}
			}
		}(worker)
	}
	wg.Wait()
	if len(c.entries) > directCoinCacheEntries || c.bytes > directCoinCacheBytes {
		t.Fatal("concurrent inserts exceeded cache bounds")
	}
}

func TestDirectCoinStatusCacheOwnsBytesAndBounds(t *testing.T) {
	var c directCoinStatusCache
	key, raw := []byte("coin"), []byte(`{"Recovered":true}`)
	c.remember(key, raw, directCoinStatus{Recovered: true})
	key[0] = 'X'
	raw[0] = 'X'
	if got, ok := c.lookup([]byte("coin"), []byte(`{"Recovered":true}`)); !ok || !got.Recovered {
		t.Fatal("cache did not own its bytes")
	}
	if _, ok := c.lookup([]byte("coin"), []byte(`{"Recovered":false}`)); ok {
		t.Fatal("accepted changed bytes")
	}
	for i := 0; i < directCoinCacheEntries*2; i++ {
		c.remember([]byte(fmt.Sprint(i)), []byte("{}"), directCoinStatus{})
	}
	if len(c.entries) > directCoinCacheEntries || c.bytes > directCoinCacheBytes {
		t.Fatal("entry limit exceeded")
	}
	for i := 0; i < 12; i++ {
		c.remember([]byte(fmt.Sprintf("large%d", i)), bytes.Repeat([]byte("x"), directCoinCacheBytes/3), directCoinStatus{})
	}
	if c.bytes > directCoinCacheBytes {
		t.Fatal("byte limit exceeded")
	}
	c.remember([]byte("oversize"), bytes.Repeat([]byte("x"), directCoinCacheBytes+1), directCoinStatus{})
	if _, ok := c.entries["oversize"]; ok {
		t.Fatal("oversize entry retained")
	}
}

func TestDirectCoinCacheReadsCurrentCompleteRecord(t *testing.T) {
	db := store.NewMemory()
	t.Cleanup(func() { db.Close() })
	w, req := directBytesFixture(t, db)
	id := req.Tx.Body.Inputs[0].Output
	key := DirectCoinKey(id, 0)
	set := func(raw []byte) {
		t.Helper()
		if err := db.Update(func(state.ReadView) ([]state.Change, error) { return []state.Change{{Key: key, Value: raw}}, nil }); err != nil {
			t.Fatal(err)
		}
	}
	coin := DirectCoin{Output: req.Tx.Claims[0].Output, Certificate: &protocol.OutputCertificate{}}
	raw, _ := json.Marshal(coin)
	set(raw)
	got, found, err := w.ReadDirectCoin(id, 0)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	got.Recovered = true
	got.Certificate.Summary.Epoch = 100
	if final, err := w.DirectFinal(id, 0); err != nil || final {
		t.Fatal(final, err)
	}
	coin.Recovered = true
	raw, _ = json.Marshal(coin)
	set(raw)
	if b, err := w.SaveDirectRequestBytes(req); !errors.Is(err, protocol.ErrAuth) || b != nil {
		t.Fatal("stale status permitted recovered spend", err)
	}
	for _, bad := range []string{`{"Output":"invalid","Recovered":false}`, `{"Certificate":"invalid","Recovered":false}`, `{"Final":"invalid"}`, `{"Index":-1}`, `{`} {
		set([]byte(bad))
		if _, err := w.DirectFinal(id, 0); err == nil {
			t.Fatalf("ignored malformed full record: %s", bad)
		}
		if _, err := w.SaveDirectRequestBytes(req); err == nil {
			t.Fatalf("save ignored malformed record: %s", bad)
		}
	}
	coin.Recovered = false
	coin.Final = protocol.Hash{1}
	raw, _ = json.Marshal(coin)
	set(raw)
	if final, err := w.DirectFinal(id, 0); err != nil || !final {
		t.Fatal("new final state missed", final, err)
	}
	if err := db.Update(func(state.ReadView) ([]state.Change, error) { return []state.Change{{Key: key, Delete: true}}, nil }); err != nil {
		t.Fatal(err)
	}
	if final, err := w.DirectFinal(id, 0); err != nil || final {
		t.Fatal("cached deleted coin", final, err)
	}
	db.Close()
	if _, err := w.DirectFinal(id, 0); err == nil {
		t.Fatal("cached away store failure")
	}
}
