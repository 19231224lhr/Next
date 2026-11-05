package store

import (
	"errors"
	"fmt"
	"path/filepath"
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

// Hold the actual transaction after evaluating changes, before committing them.
type commitGate struct {
	Store
	evaluated chan struct{}
	release   chan error
}

func (s *commitGate) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	return s.Store.Update(func(v state.ReadView) ([]state.Change, error) {
		changes, err := fn(v)
		s.evaluated <- struct{}{}
		if failure := <-s.release; failure != nil {
			return nil, failure
		}
		return changes, err
	})
}

func TestGroupAcknowledgesOnlyAfterCommit(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			base := &commitGate{Store: NewMemory(), evaluated: make(chan struct{}, 1), release: make(chan error, 2)}
			g, err := NewGroup(base, 3, 3)
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			defer func() { base.release <- nil; base.release <- nil }()
			first := make(chan error, 1)
			go func() { first <- g.Update(func(state.ReadView) ([]state.Change, error) { return nil, nil }) }()
			<-base.evaluated
			done := make(chan error, 3)
			rejected := errors.New("rejected")
			for i := 0; i < 3; i++ {
				go func(i int) {
					done <- g.Update(func(v state.ReadView) ([]state.Change, error) {
						changes := []state.Change{{Key: []byte(fmt.Sprint(i)), Value: []byte("saved")}}
						if i == 1 {
							return changes, rejected
						}
						return changes, nil
					})
				}(i)
				deadline := time.Now().Add(time.Second)
				for len(g.queue) != i+1 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if len(g.queue) != i+1 {
					t.Fatal("update did not queue")
				}
			}
			base.release <- nil
			if err := <-first; err != nil {
				t.Fatal(err)
			}
			<-base.evaluated
			select {
			case err := <-done:
				t.Fatalf("returned before commit: %v", err)
			default:
			}
			failure := errors.New("commit failed")
			if fail {
				base.release <- failure
			} else {
				base.release <- nil
			}
			var successes, rejections int
			for i := 0; i < 3; i++ {
				err := <-done
				if fail {
					if !errors.Is(err, failure) {
						t.Fatal(err)
					}
					continue
				}
				if err == nil {
					successes++
				} else if errors.Is(err, rejected) {
					rejections++
				} else {
					t.Fatal(err)
				}
			}
			if !fail && (successes != 2 || rejections != 1) {
				t.Fatal(successes, rejections)
			}
			if err := g.View(func(v state.ReadView) error {
				for i := 0; i < 3; i++ {
					_, err := v.Get([]byte(fmt.Sprint(i)))
					if fail || i == 1 {
						if !errors.Is(err, state.ErrNotFound) {
							return fmt.Errorf("partial write: %d", i)
						}
					} else if err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGroupCloseDrainsFullQueue(t *testing.T) {
	base := &countedStore{Store: NewMemory(), entered: make(chan struct{}), release: make(chan struct{})}
	g, err := NewGroup(base, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	write := func() {
		done <- g.Update(func(state.ReadView) ([]state.Change, error) {
			return []state.Change{{Key: []byte("saved"), Value: []byte("yes")}}, nil
		})
	}
	go write()
	<-base.entered
	go write()
	deadline := time.Now().Add(time.Second)
	for len(g.queue) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(g.queue) != 1 {
		close(base.release)
		t.Fatal("queue not full")
	}
	closed := make(chan error, 1)
	go func() { closed <- g.Close() }()
	close(base.release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close hung")
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGroupPreservesUncertainStorageFailure(t *testing.T) {
	base, err := Open(filepath.Join(t.TempDir(), "gateway.db"), Identity{"test", "gateway", "node", 4})
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewGroup(base, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	// An unavailable underlying database is a storage error, not a business rejection.
	if err := base.db.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		err := g.Update(func(state.ReadView) ([]state.Change, error) {
			t.Error("ran a callback after storage failure")
			return nil, nil
		})
		if !errors.Is(err, ErrUncertain) {
			t.Fatalf("lost storage failure: %v", err)
		}
	}
}
