package gateway

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

type countedInstaller struct {
	calls atomic.Int32
	fail  atomic.Bool
}

func (c *countedInstaller) Approve(context.Context, protocol.PaymentRequest) (protocol.Approval, error) {
	return protocol.Approval{}, protocol.ErrUnsupported
}
func (c *countedInstaller) Install(context.Context, protocol.TXCer) error {
	c.calls.Add(1)
	if c.fail.Load() {
		return context.DeadlineExceeded
	}
	return nil
}

func TestInstallHintsSuppressOnlyAcknowledgedTargets(t *testing.T) {
	f := testkit.NewFixture("install-hints", "a", 1)
	c, e := f.Certify(f.Transaction(0, 1))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := c.MarshalBinary()
	db := store.NewMemory()
	defer db.Close()
	key := state.Key(state.KeyOutbox, c.QC.Fact[:])
	pending := state.Outbox{Fact: c.QC.Fact, Certificate: raw, Origin: f.Org.Org}
	if e = db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		e := state.Put(o, key, pending)
		return o.Changes(), e
	}); e != nil {
		t.Fatal(e)
	}
	var clients [4]MemberClient
	var counters [4]*countedInstaller
	for i := range clients {
		counters[i] = new(countedInstaller)
		clients[i] = counters[i]
	}
	counters[3].fail.Store(true)
	r := Relay{DB: db, Public: new(retryPublic), Organizations: map[protocol.Hash]protocol.OrgConfig{f.Org.Hash(): f.Org}, Members: map[protocol.Hash][4]MemberClient{f.Org.Org: clients}}
	deliver := func() {
		t.Helper()
		if e := r.deliver(context.Background(), key, pending); e == nil {
			t.Fatal("INSTALL ACK completed an unsettled payment")
		}
	}
	deliver()
	deliver()
	for _, c := range counters {
		if c.calls.Load() != 1 {
			t.Fatal("INSTALL repeated before retry interval")
		}
	}
	r.installMu.Lock()
	p := r.installs[c.QC.Fact]
	p.next = time.Time{}
	r.installs[c.QC.Fact] = p
	r.installMu.Unlock()
	counters[3].fail.Store(false)
	deliver()
	for i, c := range counters {
		want := int32(1)
		if i == 3 {
			want = 2
		}
		if c.calls.Load() != want {
			t.Fatal("acknowledged target retried or missing target skipped", i, c.calls.Load())
		}
	}
	if e = db.View(func(v state.ReadView) error { _, e := v.Get(key); return e }); e != nil {
		t.Fatal("ACK removed durable obligation", e)
	}
	// Restart loses only the hints; the durable queue still drives delivery.
	r.installs = nil
	deliver()
	for i, c := range counters {
		want := int32(2)
		if i == 3 {
			want = 3
		}
		if c.calls.Load() != want {
			t.Fatal("restart did not retry", i)
		}
	}
}
