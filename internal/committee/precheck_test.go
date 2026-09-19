package committee

import (
	"encoding/binary"
	"sync"
	"testing"
	"utxo/internal/rules"
	"utxo/protocol"
)

func TestVerificationCacheOversizePreservesEntries(t *testing.T) {
	var c verificationCache[rules.VerifiedCertificate]
	small, large := protocol.Digest("small"), protocol.Digest("large")
	c.put(small, rules.VerifiedCertificate{}, 10)
	c.put(large, rules.VerifiedCertificate{}, (8<<20)+1)
	if _, ok := c.get(small); !ok {
		t.Fatal("oversized entry evicted a reusable result")
	}
	if _, ok := c.get(large); ok {
		t.Fatal("oversized entry exceeded the cache byte budget")
	}
}

func TestVerificationCacheBoundsAndConcurrentInsert(t *testing.T) {
	var c verificationCache[int]
	key := protocol.Digest("same")
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.put(key, 7, 100); c.get(key) }()
	}
	wg.Wait()
	if len(c.order) != 1 || c.bytes != 100 {
		t.Fatal("duplicate insertion changed accounting")
	}
	for i := 0; i < verificationCacheEntries; i++ {
		var k protocol.Hash
		binary.BigEndian.PutUint64(k[:], uint64(i))
		c.put(k, i, 1)
	}
	if _, ok := c.get(key); ok {
		t.Fatal("FIFO did not evict oldest entry")
	}
	_, _, evicted, _, entries, size := c.stats()
	if entries != verificationCacheEntries || size != verificationCacheEntries || evicted != 1 {
		t.Fatal("entry bound/accounting")
	}
	c.put(key, 8, verificationCacheBytes)
	if _, ok := c.get(key); !ok || c.bytes != verificationCacheBytes || len(c.entries) != 1 {
		t.Fatal("exact byte limit not respected")
	}
	c.put(protocol.Digest("next"), 9, 1)
	if _, ok := c.get(key); ok || c.bytes != 1 || len(c.entries) != 1 {
		t.Fatal("byte eviction failed")
	}
}
