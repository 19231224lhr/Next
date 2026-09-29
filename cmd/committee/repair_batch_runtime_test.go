//go:build comet_v3

package main

import (
	"bytes"
	"context"
	"testing"
	"time"
	"utxo/crypto/chameleon"
	"utxo/internal/redaction"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestRepairPendingHasFixedLease(t *testing.T) {
	now := time.Unix(100, 0)
	p := repairPending{expires: now.Add(5 * time.Second), next: now}
	if !p.due(now) {
		t.Fatal("first attempt delayed")
	}
	p.attempted(now)
	if p.due(now.Add(time.Second)) {
		t.Fatal("early retry")
	}
	p.attempted(now.Add(4 * time.Second))
	if !p.expired(now.Add(5 * time.Second)) {
		t.Fatal("accepted retries extended lease")
	}
}

func testRepairWorker(db store.Store) *repairWorker {
	return &repairWorker{db: db, pending: make(map[int64]*repairPending), cache: newRepairOpeningCache(256), failures: make(map[protocol.OutputID]time.Time), single: make(map[int64]time.Time), head: func() (int64, int64) { return 100, 100 }}
}

func TestRepairScanPassesBadHead(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	// Missing obligations deliberately make construction fail. The scheduler
	// must still visit the tail instead of repeatedly reading the first page.
	if err := db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		for i := 1; i <= 80; i++ {
			ob := rules.DirectObligation{Output: protocol.OutputID{byte(i)}, Transaction: protocol.TxID{byte(i)}, Deadline: 1}
			if err := state.Put(o, rules.DirectDueKey(1, ob.Output), ob); err != nil {
				return nil, err
			}
			if err := state.Put(o, redaction.LocationKey(ob.Transaction), redaction.Location{Height: int64(i)}); err != nil {
				return nil, err
			}
		}
		return o.Changes(), nil
	}); err != nil {
		t.Fatal(err)
	}
	w := testRepairWorker(db)
	for i := 0; i < 10; i++ {
		w.step(context.Background())
	}
	if _, ok := w.failures[protocol.OutputID{80}]; !ok {
		t.Fatal("tail never considered after bad head")
	}
	if len(w.pending) != 0 {
		t.Fatal("invalid tasks retained candidate slots")
	}
	for i := 0; i < 1000; i++ {
		w.failed(protocol.OutputID{byte(i), byte(i >> 8)}, time.Now())
		w.fallback(int64(i), time.Now())
	}
	if len(w.failures) > 256 || len(w.single) > 256 {
		t.Fatal("unbounded retry hints")
	}
}

func TestRepairAcceptedRetriesKeepBytesAndStopOnPublicCompletion(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	c := protocol.RepairBatch{Network: protocol.Hash{1}, Height: 1, Items: []protocol.RepairItem{{Output: protocol.OutputID{2}}}, Parts: []chameleon.Opening{{}}}
	raw, err := c.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		e := state.Put(o, rules.DirectObligationKey(c.Items[0].Output), rules.DirectObligation{Status: rules.DirectOpen})
		return o.Changes(), e
	}); err != nil {
		t.Fatal(err)
	}
	w := testRepairWorker(db)
	calls := 0
	w.submit = func(_ context.Context, b []byte) (uint32, error) {
		calls++
		if !bytes.Equal(b, raw) {
			t.Fatal("retry rebuilt envelope")
		}
		return 0, nil
	}
	expires := time.Now().Add(5 * time.Second)
	p := &repairPending{command: c, raw: raw, expires: expires}
	w.pending[1] = p
	w.step(context.Background())
	p.next = time.Now().Add(-time.Second)
	w.step(context.Background())
	if calls != 2 || p.expires != expires {
		t.Fatal("acceptance suppressed retries or renewed lease")
	}
	if err = db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		e := state.Put(o, redaction.BatchTaskKey(c.ID()), redaction.BatchTask{Command: c})
		return o.Changes(), e
	}); err != nil {
		t.Fatal(err)
	}
	w.step(context.Background())
	if len(w.pending) != 0 || calls != 2 {
		t.Fatal("public completion did not stop candidate")
	}
}
func TestRepairOpeningCacheBounded(t *testing.T) {
	c := newRepairOpeningCache(2)
	c.put(protocol.Hash{1}, chameleon.Opening{1})
	c.put(protocol.Hash{2}, chameleon.Opening{2})
	c.put(protocol.Hash{3}, chameleon.Opening{3})
	if _, ok := c.values[protocol.Hash{1}]; ok {
		t.Fatal("old result not evicted")
	}
	if len(c.values) != 2 {
		t.Fatal("unbounded cache")
	}
	c.put(protocol.Hash{3}, chameleon.Opening{4})
	if len(c.values) != 2 || c.values[protocol.Hash{3}][0] != 4 {
		t.Fatal("replacement")
	}
}
