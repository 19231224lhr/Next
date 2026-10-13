package gateway

import (
	"bytes"
	"container/list"
	"encoding/json"

	"utxo/internal/state"
)

// directOutboxCache belongs to one relay event loop. Every lookup still uses
// the current durable bytes; caching never determines whether a task exists.
type directOutboxCache struct {
	entries                     map[string]*list.Element
	order                       list.List
	bytes, maxEntries, maxBytes int
}

type directOutboxEntry struct {
	key     string
	raw     []byte
	pending state.Outbox
	size    int
}

func newDirectOutboxCache(entries, size int) *directOutboxCache {
	return &directOutboxCache{entries: make(map[string]*list.Element), maxEntries: entries, maxBytes: size}
}

func (c *directOutboxCache) remove(e *list.Element) {
	entry := e.Value.(directOutboxEntry)
	delete(c.entries, entry.key)
	c.bytes -= entry.size
	c.order.Remove(e)
}

func (c *directOutboxCache) decode(key, raw []byte) (state.Outbox, error) {
	pending, err := c.snapshot(key, raw)
	if err != nil {
		return pending, err
	}
	return cloneDirectOutbox(pending), nil
}

// snapshot returns read-only cache-owned slices. Its sole production caller is
// relay refill: scheduling reads metadata, and prepareDirectPending owns a clone
// before verification or transport. Replacement/eviction never mutates snapshots.
func (c *directOutboxCache) snapshot(key, raw []byte) (state.Outbox, error) {
	k := string(key)
	if e := c.entries[k]; e != nil {
		entry := e.Value.(directOutboxEntry)
		if bytes.Equal(entry.raw, raw) {
			c.order.MoveToBack(e)
			return entry.pending, nil
		}
		c.remove(e)
	}
	var pending state.Outbox
	if err := json.Unmarshal(raw, &pending); err != nil {
		return pending, err
	}
	size := len(k) + len(raw) + len(pending.Certificate) + len(pending.Attempt)
	if c.maxEntries > 0 && size <= c.maxBytes {
		for len(c.entries) >= c.maxEntries || c.bytes+size > c.maxBytes {
			c.remove(c.order.Front())
		}
		c.entries[k] = c.order.PushBack(directOutboxEntry{k, bytes.Clone(raw), pending, size})
		c.bytes += size
	}
	return pending, nil
}

// Callers own returned slices, so a worker cannot change cached evidence.
func cloneDirectOutbox(p state.Outbox) state.Outbox {
	p.Certificate = bytes.Clone(p.Certificate)
	p.Attempt = bytes.Clone(p.Attempt)
	return p
}
