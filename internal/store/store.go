package store

import (
	"bytes"
	"errors"
	"sync"
	"utxo/internal/state"
)

type Identity struct {
	Network, Role, Node string
	Schema              uint64
}

var ErrIdentity = errors.New("database identity mismatch")
var ErrClosed = errors.New("store closed")
var ErrUncertain = errors.New("commit result uncertain; stop signing")

type Store interface {
	View(func(state.ReadView) error) error
	Update(func(state.ReadView) ([]state.Change, error)) error
	Close() error
}
type memoryView map[string][]byte

func (v memoryView) Get(k []byte) ([]byte, error) {
	b, ok := v[string(k)]
	if !ok {
		return nil, state.ErrNotFound
	}
	return bytes.Clone(b), nil
}

type Memory struct {
	mu     sync.RWMutex
	data   memoryView
	closed bool
}

func NewMemory() *Memory { return &Memory{data: make(memoryView)} }
func (m *Memory) View(fn func(state.ReadView) error) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return ErrClosed
	}
	return fn(m.data)
}
func (m *Memory) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	cs, e := fn(m.data)
	if e != nil {
		return e
	}
	for _, c := range cs {
		if len(c.Key) == 0 {
			return errors.New("empty state key")
		}
	}
	for _, c := range cs {
		if c.Delete {
			delete(m.data, string(c.Key))
		} else {
			m.data[string(c.Key)] = bytes.Clone(c.Value)
		}
	}
	return nil
}
func (m *Memory) Close() error { m.mu.Lock(); defer m.mu.Unlock(); m.closed = true; return nil }
