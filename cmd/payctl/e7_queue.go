package main

import (
	"sync"
	"time"
	"utxo/protocol"
)

type e7Task struct {
	Lane, Generation, Hop, Owner, Parent int
	Input                                protocol.OutputID
	receivedAt                           time.Time
}
type e7Ready struct {
	mu       sync.Mutex
	tasks    []e7Task
	last     map[int]int
	capacity int
}

func newE7Ready(capacity int) *e7Ready { return &e7Ready{last: map[int]int{}, capacity: capacity} }
func (q *e7Ready) add(t e7Task) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	version := t.Generation*10 + t.Hop
	if prev, ok := q.last[t.Lane]; ok && prev >= version {
		return false
	}
	if len(q.tasks) >= q.capacity {
		return false
	}
	q.last[t.Lane] = version
	q.tasks = append(q.tasks, t)
	return true
}
func (q *e7Ready) take() (e7Task, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.tasks) == 0 {
		return e7Task{}, false
	}
	t := q.tasks[0]
	q.tasks = q.tasks[1:]
	return t, true
}
func (q *e7Ready) len() int { q.mu.Lock(); defer q.mu.Unlock(); return len(q.tasks) }
func e7Recipient(owner, wallets, seed int, cross bool) int {
	step := 2 * (seed%(wallets/2-1) + 1)
	if cross {
		step = 2*(seed%(wallets/2)) + 1
	}
	return (owner + step) % wallets
}
