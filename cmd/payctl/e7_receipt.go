package main

import (
	"sync"
	"utxo/protocol"
)

// e7Ack is returned only after the receiving wallet has accepted the output.
// ReceiveNS is a local elapsed duration, never a cross-process clock difference.
type e7Ack struct {
	Index     int
	ReceiveNS int64
	Fact      protocol.SpendFactID
}

type e7ReceiptFlight struct {
	done chan struct{}
	ack  e7Ack
	err  error
}

type e7Receipts struct {
	mu      sync.Mutex
	entries map[int]*e7ReceiptFlight
}

func newE7Receipts() *e7Receipts {
	return &e7Receipts{entries: make(map[int]*e7ReceiptFlight)}
}

// Concurrent deliveries share one receive operation. Failed validation does not
// poison the identity: a later valid delivery may retry it.
func (r *e7Receipts) accept(index int, receive func() (e7Ack, error)) (e7Ack, error) {
	r.mu.Lock()
	if f, ok := r.entries[index]; ok {
		r.mu.Unlock()
		<-f.done
		return f.ack, f.err
	}
	f := &e7ReceiptFlight{done: make(chan struct{})}
	r.entries[index] = f
	r.mu.Unlock()
	f.ack, f.err = receive()
	r.mu.Lock()
	if f.err != nil {
		delete(r.entries, index)
	}
	close(f.done)
	r.mu.Unlock()
	return f.ack, f.err
}
