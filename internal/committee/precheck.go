package committee

import (
	"sync"
	"utxo/internal/rules"
	"utxo/protocol"
)

type cacheEntry struct {
	key   protocol.Hash
	value rules.VerifiedCertificate
	bytes int
}
type certificateCache struct {
	mu      sync.Mutex
	entries map[protocol.Hash]cacheEntry
	order   []protocol.Hash
	bytes   int
}

func (c *certificateCache) get(key protocol.Hash) (rules.VerifiedCertificate, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	return entry.value, ok
}
func (c *certificateCache) put(key protocol.Hash, value rules.VerifiedCertificate, size int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[protocol.Hash]cacheEntry)
	}
	if _, exists := c.entries[key]; exists {
		return
	}
	for len(c.order) > 0 && (len(c.order) >= 2048 || c.bytes+size > 8<<20) {
		old := c.order[0]
		c.order = c.order[1:]
		c.bytes -= c.entries[old].bytes
		delete(c.entries, old)
	}
	c.entries[key] = cacheEntry{key, value, size}
	c.order = append(c.order, key)
	c.bytes += size
}
