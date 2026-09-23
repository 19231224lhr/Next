package main

import (
	"context"
	"errors"
	"math/bits"
	"sync"
	"time"

	"utxo/protocol"
)

var errE5GateFull = errors.New("E5 delivery gate capacity reached")

type e5RequestKey struct {
	Network protocol.Hash
	Tx      protocol.TxID
}

type e5GateEntry struct {
	digest     [32]byte
	fact       protocol.SpendFactID
	hasFact    bool
	stored     uint8
	storedAt   [4]int64
	install3At time.Time
	publicAt   time.Time
	changed    chan struct{}
}

type e5Gate struct {
	mu      sync.Mutex
	limit   int
	entries map[e5RequestKey]*e5GateEntry
}

func newE5Gate(limit int) *e5Gate {
	return &e5Gate{limit: limit, entries: make(map[e5RequestKey]*e5GateEntry, limit)}
}

func (g *e5Gate) Register(key e5RequestKey, digest [32]byte) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e := g.entries[key]; e != nil {
		if e.digest != digest {
			return false, errors.New("E5 Network/TxID reused with different signed request")
		}
		return false, nil
	}
	if len(g.entries) >= g.limit {
		return false, errE5GateFull
	}
	g.entries[key] = &e5GateEntry{digest: digest, changed: make(chan struct{})}
	return true, nil
}

func (g *e5Gate) Stored(key e5RequestKey, digest [32]byte, fact protocol.SpendFactID, member uint16) error {
	if member >= 4 {
		return errors.New("E5 member index out of range")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.entries[key]
	if e == nil || e.digest != digest {
		return errors.New("E5 INSTALL does not match registered request")
	}
	if e.hasFact && e.fact != fact {
		return errors.New("E5 request bound to conflicting fact")
	}
	e.fact, e.hasFact = fact, true
	bit := uint8(1 << member)
	if e.stored&bit != 0 {
		return nil
	}
	e.stored |= bit
	e.storedAt[member] = time.Now().UnixNano()
	if bits.OnesCount8(e.stored) >= 3 && e.install3At.IsZero() {
		e.install3At = time.Now()
	}
	close(e.changed)
	e.changed = make(chan struct{})
	return nil
}

func (g *e5Gate) Public(key e5RequestKey, digest [32]byte, fact protocol.SpendFactID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.entries[key]
	if e == nil || e.digest != digest {
		return errors.New("E5 public result does not match registered request")
	}
	if e.hasFact && e.fact != fact {
		return errors.New("E5 request bound to conflicting public fact")
	}
	e.fact, e.hasFact = fact, true
	if e.publicAt.IsZero() {
		e.publicAt = time.Now()
		close(e.changed)
		e.changed = make(chan struct{})
	}
	return nil
}

func (g *e5Gate) Ready(key e5RequestKey) (bool, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.entries[key]
	if e == nil {
		return false, ""
	}
	return e.ready()
}

func (e *e5GateEntry) ready() (bool, string) {
	if !e.install3At.IsZero() && !e.publicAt.IsZero() {
		if e.install3At.Equal(e.publicAt) {
			return true, "tie"
		}
		if e.install3At.Before(e.publicAt) {
			return true, "install3"
		}
		return true, "public"
	}
	if !e.install3At.IsZero() {
		return true, "install3"
	}
	if !e.publicAt.IsZero() {
		return true, "public"
	}
	return false, ""
}

func (g *e5Gate) Wait(ctx context.Context, key e5RequestKey) (string, error) {
	for {
		g.mu.Lock()
		e := g.entries[key]
		if e == nil {
			g.mu.Unlock()
			return "", errors.New("E5 request was not registered")
		}
		if ready, reason := e.ready(); ready {
			g.mu.Unlock()
			return reason, nil
		}
		changed := e.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-changed:
		}
	}
}

func (g *e5Gate) Bind(key e5RequestKey, fact protocol.SpendFactID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.entries[key]
	if e == nil || e.hasFact && e.fact != fact {
		return errors.New("E5 certificate fact conflict")
	}
	e.fact, e.hasFact = fact, true
	return nil
}
