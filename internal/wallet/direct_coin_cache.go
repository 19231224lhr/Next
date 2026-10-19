package wallet

import (
	"bytes"
	"container/list"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"unsafe"
	"utxo/internal/state"
	"utxo/protocol"
)

const directCoinCacheEntries = 256
const directCoinCacheBytes = 4 << 20

type directCoinStatus struct {
	Final     protocol.Hash
	Recovered bool
}
type directCoinStatusEntry struct {
	key    string
	raw    []byte
	status directCoinStatus
	coin   *DirectCoin
	size   int
}

// The cache carries no authorization. A hit requires reading the current store
// value and comparing every byte with a previously fully decoded record.
type directCoinStatusCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   list.List
	bytes   int
}

func (c *directCoinStatusCache) lookup(key, raw []byte) (directCoinStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[string(key)]; e != nil {
		item := e.Value.(directCoinStatusEntry)
		if bytes.Equal(item.raw, raw) {
			c.order.MoveToFront(e)
			return item.status, true
		}
	}
	return directCoinStatus{}, false
}
func (c *directCoinStatusCache) remember(key, raw []byte, status directCoinStatus) {
	c.rememberRecord(key, raw, status, nil)
}

func cloneDirectCoin(coin DirectCoin) DirectCoin {
	if coin.Certificate != nil {
		cert := *coin.Certificate
		cert.Summary.Outputs = slices.Clone(cert.Summary.Outputs)
		cert.Summary.Admission = slices.Clone(cert.Summary.Admission)
		cert.Summary.Grants = slices.Clone(cert.Summary.Grants)
		cert.QC.Votes = slices.Clone(cert.QC.Votes)
		coin.Certificate = &cert
	}
	return coin
}

func (c *directCoinStatusCache) lookupCoin(key, raw []byte) (DirectCoin, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[string(key)]; e != nil {
		item := e.Value.(directCoinStatusEntry)
		if item.coin != nil && bytes.Equal(item.raw, raw) {
			c.order.MoveToFront(e)
			return cloneDirectCoin(*item.coin), true
		}
	}
	return DirectCoin{}, false
}

func (c *directCoinStatusCache) rememberCoin(key, raw []byte, coin DirectCoin) {
	c.rememberRecord(key, raw, directCoinStatus{Final: coin.Final, Recovered: coin.Recovered}, &coin)
}

func (c *directCoinStatusCache) rememberRecord(key, raw []byte, status directCoinStatus, coin *DirectCoin) {
	size := len(key) + len(raw)
	if coin != nil {
		size += int(unsafe.Sizeof(*coin))
		if cert := coin.Certificate; cert != nil {
			size += int(unsafe.Sizeof(*cert)) + len(cert.Summary.Outputs)*int(unsafe.Sizeof(protocol.OutputCommitment{})) + len(cert.Summary.Admission)*int(unsafe.Sizeof(protocol.Allocation{})) + len(cert.Summary.Grants)*int(unsafe.Sizeof(protocol.Hash{})) + len(cert.QC.Votes)*int(unsafe.Sizeof(protocol.SpendVote{}))
		}
	}
	if size > directCoinCacheBytes {
		return
	}
	if coin != nil {
		owned := cloneDirectCoin(*coin)
		coin = &owned
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]*list.Element)
	}
	k := string(key)
	remove := func(e *list.Element) {
		item := e.Value.(directCoinStatusEntry)
		delete(c.entries, item.key)
		c.bytes -= item.size
		c.order.Remove(e)
	}
	if e := c.entries[k]; e != nil {
		remove(e)
	}
	for len(c.entries) >= directCoinCacheEntries || c.bytes+size > directCoinCacheBytes {
		remove(c.order.Back())
	}
	c.entries[k] = c.order.PushFront(directCoinStatusEntry{key: k, raw: bytes.Clone(raw), status: status, coin: coin, size: size})
	c.bytes += size
}

func (w *Wallet) loadDirectCoin(v state.ReadView, key []byte) (DirectCoin, bool, error) {
	var coin DirectCoin
	raw, err := v.Get(key)
	if errors.Is(err, state.ErrNotFound) {
		return coin, false, nil
	}
	if err != nil {
		return coin, false, err
	}
	if cached, hit := w.coinStatus.lookupCoin(key, raw); hit {
		return cached, true, nil
	}
	if err = json.Unmarshal(raw, &coin); err != nil {
		return coin, false, err
	}
	w.coinStatus.rememberCoin(key, raw, coin)
	return coin, true, nil
}

// ReadDirectCoin returns an owned copy. Cached decoding is reused only after
// reading and matching the complete current store value.
func (w *Wallet) ReadDirectCoin(id protocol.OutputID, instance uint8) (DirectCoin, bool, error) {
	var coin DirectCoin
	var found bool
	err := w.db.View(func(v state.ReadView) error {
		var err error
		coin, found, err = w.loadDirectCoin(v, DirectCoinKey(id, instance))
		return err
	})
	return coin, found, err
}

func (w *Wallet) loadDirectCoinStatus(v state.ReadView, key []byte) (directCoinStatus, bool, error) {
	raw, err := v.Get(key)
	if errors.Is(err, state.ErrNotFound) {
		return directCoinStatus{}, false, nil
	}
	if err != nil {
		return directCoinStatus{}, false, err
	}
	if status, ok := w.coinStatus.lookup(key, raw); ok {
		return status, true, nil
	}
	// Decode the entire schema: projecting only Final/Recovered would accept
	// invalid known fields such as a malformed certificate or output.
	var coin DirectCoin
	if err = json.Unmarshal(raw, &coin); err != nil {
		return directCoinStatus{}, false, err
	}
	status := directCoinStatus{Final: coin.Final, Recovered: coin.Recovered}
	w.coinStatus.remember(key, raw, status)
	return status, true, nil
}
