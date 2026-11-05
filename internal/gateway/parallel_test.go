package gateway

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
	"utxo/finality"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

type waitingPublic struct {
	started chan protocol.Hash
	active  atomic.Int32
}

func (*waitingPublic) Submit(context.Context, []byte) error { return nil }
func (*waitingPublic) Certificate(context.Context, protocol.SpendFactID) (protocol.TXCer, error) {
	return protocol.TXCer{}, state.ErrNotFound
}
func (p *waitingPublic) Receipt(ctx context.Context, _ protocol.FactKind, key protocol.Hash) (finality.FactProof, error) {
	p.active.Add(1)
	defer p.active.Add(-1)
	p.started <- key
	<-ctx.Done()
	return finality.FactProof{}, ctx.Err()
}

func TestRelayBoundsConcurrencyAndJoinsOnCancellation(t *testing.T) {
	f := testkit.NewFixture("relay-workers", "a", 8)
	db := store.NewMemory()
	defer db.Close()
	for i := 0; i < 8; i++ {
		c, e := f.Certify(f.Transaction(i, 1))
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := c.MarshalBinary()
		if e = db.Update(func(v state.ReadView) ([]state.Change, error) {
			o := state.NewOverlay(v)
			e := state.Put(o, state.Key(state.KeyOutbox, c.QC.Fact[:]), state.Outbox{Fact: c.QC.Fact, Certificate: raw, Origin: f.Org.Org})
			return o.Changes(), e
		}); e != nil {
			t.Fatal(e)
		}
	}
	fact := protocol.FinalFact{Network: f.Org.Network, Kind: protocol.FactFeeClosed, Key: protocol.Digest("test"), Revision: 1, Rules: f.Schedule.IDs(), Payload: []byte("test")}
	trust, _, e := testkit.Proof("relay-workers", fact)
	if e != nil {
		t.Fatal(e)
	}
	public := &waitingPublic{started: make(chan protocol.Hash, 16)}
	r := Relay{DB: db, Public: public, Trust: trust, Organizations: map[protocol.Hash]protocol.OrgConfig{f.Org.Hash(): f.Org}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	seen := map[protocol.Hash]bool{}
	for i := 0; i < 4; i++ {
		select {
		case id := <-public.started:
			if seen[id] {
				t.Fatal("same task overlaps")
			}
			seen[id] = true
		case <-time.After(800 * time.Millisecond):
			t.Fatal("four independent tasks did not start")
		}
	}
	select {
	case <-public.started:
		t.Fatal("more than four relay tasks started")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not stop")
	}
	if public.active.Load() != 0 {
		t.Fatal("Run returned before its workers stopped")
	}
	entries, e := store.Scan(db, state.Key(state.KeyOutbox), nil, 16)
	if e != nil || len(entries) != 8 {
		t.Fatal("cancellation discarded obligations", len(entries), e)
	}
}
