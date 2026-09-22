package gateway

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

// A quorum failure may already have occupied two members. A different payment
// must not spread more partial approvals while the original one is retried.
func TestSerialDirectKeepsFailedHeadUntilValidQuorum(t *testing.T) {
	c, _, payments := directRelayFixture(t, 2)
	c.db = store.NewMemory()
	t.Cleanup(func() { c.db.Close() })
	c.EnableSerialDirect()
	a := protocol.DirectRequest{Tx: payments[0].Tx}
	b := protocol.DirectRequest{Tx: payments[1].Tx}
	var recoverThird atomic.Bool
	var bCalls atomic.Int64
	for i := range c.members {
		c.members[i] = encodedDirectClient{call: func(ctx context.Context, raw []byte) (protocol.DirectApproval, error) {
			req, err := protocol.DecodeDirectRequest(raw)
			if err != nil {
				return protocol.DirectApproval{}, err
			}
			p := payments[0]
			if req.Tx.ID() == b.Tx.ID() {
				p = payments[1]
				bCalls.Add(1)
			}
			if i >= len(p.Certificate.QC.Votes) || (req.Tx.ID() == a.Tx.ID() && i == 2 && !recoverThird.Load()) {
				return protocol.DirectApproval{}, rules.ErrLimited
			}
			return protocol.DirectApproval{Summary: p.Certificate.Summary, Vote: p.Certificate.QC.Votes[i]}, nil
		}}
	}
	if _, err := c.CollectDirect(context.Background(), a); err == nil {
		t.Fatal("accepted two votes")
	}
	if _, err := c.CollectDirect(context.Background(), b); !errors.Is(err, ErrSigningBusy) {
		t.Fatalf("new fact was not deferred: %v", err)
	}
	if bCalls.Load() != 0 {
		t.Fatal("new fact reached members")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.CollectDirect(cancelled, a); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled retry: %v", err)
	}
	if _, err := c.CollectDirect(context.Background(), b); !errors.Is(err, ErrSigningBusy) {
		t.Fatal("cancellation discarded head")
	}
	recoverThird.Store(true)
	for _, req := range []protocol.DirectRequest{a, b} {
		cert, err := c.CollectDirect(context.Background(), req)
		if err != nil || cert.Verify(c.org) != nil {
			t.Fatalf("quorum did not resume: %v", err)
		}
	}
}

func TestSerialDirectCompletedResultBypassesAnotherHead(t *testing.T) {
	c, _, payments := directRelayFixture(t, 2)
	c.EnableSerialDirect()
	id := payments[1].Tx.ID()
	if err := c.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		o.Delete(state.Key(state.KeyCollected, id[:]))
		return o.Changes(), nil
	}); err != nil {
		t.Fatal(err)
	}
	// B has no retrievable result and its peers cannot provide a quorum. A's
	// previously completed result must remain retrievable without new votes.
	if _, err := c.CollectDirect(context.Background(), protocol.DirectRequest{Tx: payments[1].Tx}); err == nil {
		t.Fatal("B unexpectedly ready")
	}
	cert, err := c.CollectDirect(context.Background(), protocol.DirectRequest{Tx: payments[0].Tx})
	if err != nil || cert.Verify(c.org) != nil {
		t.Fatalf("completed A blocked by B: %v", err)
	}
}

func TestSerialDirectInvalidOwnerDoesNotPinHead(t *testing.T) {
	c, _, payments := directRelayFixture(t, 1)
	c.EnableSerialDirect()
	bad := protocol.DirectRequest{Tx: payments[0].Tx}
	bad.Tx.Auth = append([]protocol.OwnerAuth(nil), bad.Tx.Auth...)
	bad.Tx.Auth[0].Signature[0] ^= 1
	if _, err := c.CollectDirect(context.Background(), bad); err == nil {
		t.Fatal("bad owner accepted")
	}
	// A valid request can enter; member errors here are unrelated to gate state.
	_, err := c.CollectDirect(context.Background(), protocol.DirectRequest{Tx: payments[0].Tx})
	if errors.Is(err, ErrSigningBusy) {
		t.Fatal("invalid owner pinned admission")
	}
}

func TestDirectSavedResultAndOutboxAreIndependent(t *testing.T) {
	for _, observed := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "observed"}[observed], func(t *testing.T) {
			c, _, payments := directRelayFixture(t, 1)
			p := payments[0]
			fact := p.Certificate.QC.Fact
			key := state.Key(state.KeyOutbox, fact[:])
			if err := c.db.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				o.Delete(key)
				if observed {
					if e := state.Put(o, state.Key(state.KeyObserved, fact[:]), true); e != nil {
						return nil, e
					}
				}
				return o.Changes(), nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := c.PersistDirect(p); err != nil {
				t.Fatal(err)
			}
			if err := c.db.View(func(v state.ReadView) error {
				_, found, e := state.Load[state.Outbox](v, key)
				if found == observed {
					t.Errorf("outbox exists=%v, observed=%v", found, observed)
				}
				return e
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
