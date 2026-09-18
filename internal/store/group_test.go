package store

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"utxo/internal/state"
)

type countedStore struct {
	Store
	commits atomic.Int64
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *countedStore) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	c.commits.Add(1)
	c.once.Do(func() { close(c.entered); <-c.release })
	return c.Store.Update(fn)
}
func TestGroupCommitSerialChecksAndIndependentRejection(t *testing.T) {
	base := &countedStore{Store: NewMemory(), entered: make(chan struct{}), release: make(chan struct{})}
	group, e := NewGroup(base, 64, 64)
	if e != nil {
		t.Fatal(e)
	}
	defer group.Close()
	var wg sync.WaitGroup
	var rejected atomic.Int64
	submit := func(reject bool) {
		defer wg.Done()
		err := group.Update(func(v state.ReadView) ([]state.Change, error) {
			n, _, e := state.Load[uint64](v, []byte("count"))
			if e != nil {
				return nil, e
			}
			if reject {
				return nil, errors.New("business rejection")
			}
			o := state.NewOverlay(v)
			state.Put(o, []byte("count"), n+1)
			return o.Changes(), nil
		})
		if err != nil {
			rejected.Add(1)
		}
	}
	wg.Add(1)
	go submit(false)
	<-base.entered
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go submit(i == 0)
	}
	deadline := time.Now().Add(time.Second)
	for len(group.queue) < 32 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(base.release)
	wg.Wait()
	if rejected.Load() != 1 {
		t.Fatalf("rejected %d", rejected.Load())
	}
	var count uint64
	group.View(func(v state.ReadView) error { count, _, _ = state.Load[uint64](v, []byte("count")); return nil })
	if count != 32 {
		t.Fatalf("lost atomic increments %d", count)
	}
	if base.commits.Load() >= 33 {
		t.Fatal("no grouping")
	}
	t.Logf("32 successful operations used %d durable transactions", base.commits.Load())
}
func TestGroupRejectsUseAfterClose(t *testing.T) {
	g, e := NewGroup(NewMemory(), 4, 4)
	if e != nil {
		t.Fatal(e)
	}
	if e = g.Close(); e != nil {
		t.Fatal(e)
	}
	if e = g.Update(func(state.ReadView) ([]state.Change, error) { t.Fatal("ran after close"); return nil, nil }); e == nil {
		t.Fatal("accepted after close")
	}
}
