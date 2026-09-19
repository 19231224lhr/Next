package gateway

import (
	"sync"

	"utxo/internal/state"
	"utxo/protocol"
)

// DirectInbox owns immutable early-delivery bytes until durable outbox or a
// verified block takes over. It is an optimization, never a durability receipt.
type DirectInbox struct {
	mu     sync.Mutex
	items  map[protocol.SpendFactID]state.Outbox
	bytes  int
	closed bool
	wake   chan struct{}
}

func NewDirectInbox() *DirectInbox {
	return &DirectInbox{items: make(map[protocol.SpendFactID]state.Outbox), wake: make(chan struct{}, 1)}
}

func (q *DirectInbox) Offer(p protocol.DirectPayment) bool {
	raw, err := p.MarshalBinary()
	if err != nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	fact := p.Certificate.QC.Fact
	if _, ok := q.items[fact]; ok {
		return true
	}
	// Match the handler's admission bound; large payments also have a byte cap.
	if len(q.items) >= 128 || q.bytes+len(raw) > 32<<20 {
		return false
	}
	q.items[fact] = state.Outbox{Fact: fact, Certificate: raw, Origin: p.Tx.Body.Certifier}
	q.bytes += len(raw)
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return true
}

func (q *DirectInbox) snapshot() []state.Outbox {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	items := make([]state.Outbox, 0, len(q.items))
	for _, p := range q.items {
		items = append(items, p)
	}
	return items
}
func (q *DirectInbox) get(fact protocol.SpendFactID) (state.Outbox, bool) {
	if q == nil {
		return state.Outbox{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	p, ok := q.items[fact]
	return p, ok
}
func (q *DirectInbox) forget(fact protocol.SpendFactID) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.bytes -= len(q.items[fact].Certificate)
	delete(q.items, fact)
}
func (q *DirectInbox) close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	clear(q.items)
	q.bytes = 0
}
