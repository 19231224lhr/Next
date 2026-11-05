package store

import (
	"errors"
	"sync"
	"utxo/internal/state"
)

type mutation struct {
	fn   func(state.ReadView) ([]state.Change, error)
	done chan error
}

// Group coalesces only already queued mutations. Each operation sees prior
// successful changes in the batch. Responses are released after one committed
// backing transaction; durability follows the store mode. Individual business
// rejections never merge their child overlay.
type Group struct {
	base    Store
	queue   chan mutation
	batch   int
	stopped chan struct{}
	mu      sync.RWMutex
	closed  bool
}

func NewGroup(base Store, capacity, batch int) (*Group, error) {
	if base == nil || capacity < 1 || batch < 1 || batch > capacity {
		return nil, errors.New("invalid commit queue configuration")
	}
	g := &Group{base: base, queue: make(chan mutation, capacity), batch: batch, stopped: make(chan struct{})}
	go g.run()
	return g, nil
}
func (g *Group) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.closed {
		return ErrClosed
	}
	request := mutation{fn: fn, done: make(chan error, 1)}
	g.queue <- request
	return <-request.done
}
func (g *Group) View(fn func(state.ReadView) error) error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.closed {
		return ErrClosed
	}
	return g.base.View(fn)
}
func (g *Group) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil
	}
	g.closed = true
	close(g.queue)
	<-g.stopped
	return g.base.Close()
}
func (g *Group) run() {
	defer close(g.stopped)
	for first := range g.queue {
		batch := []mutation{first}
	drain:
		for len(batch) < g.batch {
			select {
			case request, ok := <-g.queue:
				if !ok {
					break drain
				}
				batch = append(batch, request)
			default:
				break drain
			}
		}
		results := make([]error, len(batch))
		err := g.base.Update(func(v state.ReadView) ([]state.Change, error) {
			overlay := state.NewOverlay(v)
			for i, request := range batch {
				changes, e := request.fn(overlay)
				results[i] = e
				if e == nil {
					overlay.Apply(changes)
				}
			}
			return overlay.Changes(), nil
		})
		for i, request := range batch {
			if err != nil {
				request.done <- err
			} else {
				request.done <- results[i]
			}
		}
	}
}
