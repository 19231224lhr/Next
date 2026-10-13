package main

import (
	"time"
	"utxo/internal/state"
	"utxo/internal/store"
)

// chainBuildTrace is opt-in client diagnostics, not part of the payment protocol.
type chainBuildTrace struct {
	clock  func() time.Time
	last   time.Time
	stages map[string]float64
}

func newChainBuildTrace(enabled bool, clock func() time.Time) *chainBuildTrace {
	t := &chainBuildTrace{}
	if enabled {
		t.clock, t.last, t.stages = clock, clock(), make(map[string]float64)
	}
	return t
}

func (t *chainBuildTrace) mark(stage string) {
	if t.stages == nil {
		return
	}
	now := t.clock()
	t.stages[stage] = float64(now.Sub(t.last)) / 1e6
	t.last = now
}

// Used only by the serial chain client. The real store and its commit boundary
// remain unchanged; the callback timer separates validation from queue/commit time.
type chainBuildStore struct {
	store.Store
	last map[string]float64
}

func (s *chainBuildStore) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	start := time.Now()
	var entered, completed time.Time
	err := s.Store.Update(func(v state.ReadView) ([]state.Change, error) {
		entered = time.Now()
		changes, e := fn(v)
		completed = time.Now()
		return changes, e
	})
	end := time.Now()
	if !entered.IsZero() {
		s.last = map[string]float64{
			"queue_wait":           float64(entered.Sub(start)) / 1e6,
			"validation_and_locks": float64(completed.Sub(entered)) / 1e6,
			"commit_and_resume":    float64(end.Sub(completed)) / 1e6,
			"update_total":         float64(end.Sub(start)) / 1e6,
		}
	}
	return err
}
