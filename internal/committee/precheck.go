package committee

import (
	"sync"
	"utxo/protocol"
)

const verificationCacheEntries = 2048
const verificationCacheBytes = 8 << 20

type cacheEntry[T any] struct {
	value T
	bytes int
}
type verificationCache[T any] struct {
	mu       sync.Mutex
	entries  map[protocol.Hash]cacheEntry[T]
	order    []protocol.Hash
	bytes    int
	disabled bool // Experiment-only bypass; fixed before the Engine is used.

	hits, misses, evictions, oversized uint64
}

func (c *verificationCache[T]) get(key protocol.Hash) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	ok = ok && !c.disabled
	if ok {
		c.hits++
	} else {
		c.misses++
	}
	return entry.value, ok
}
func (c *verificationCache[T]) put(key protocol.Hash, value T, size int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disabled {
		return
	}
	if size > verificationCacheBytes {
		c.oversized++
		return
	}
	if c.entries == nil {
		c.entries = make(map[protocol.Hash]cacheEntry[T])
	}
	if _, exists := c.entries[key]; exists {
		return
	}
	for len(c.order) > 0 && (len(c.order) >= verificationCacheEntries || c.bytes+size > verificationCacheBytes) {
		old := c.order[0]
		c.order = c.order[1:]
		c.bytes -= c.entries[old].bytes
		delete(c.entries, old)
		c.evictions++
	}
	c.entries[key] = cacheEntry[T]{value, size}
	c.order = append(c.order, key)
	c.bytes += size
}

// stats is diagnostic only: encoded-byte accounting is not Go heap usage.
func (c *verificationCache[T]) stats() (hits, misses, evictions, oversized uint64, entries, bytes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses, c.evictions, c.oversized, len(c.entries), c.bytes
}
