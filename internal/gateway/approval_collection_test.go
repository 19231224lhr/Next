package gateway

import (
	"bytes"
	"context"
	"testing"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type approvalFeedFunc func(context.Context, []byte) (protocol.ApprovalPage, error)

func (f approvalFeedFunc) ApprovedDirect(ctx context.Context, after []byte) (protocol.ApprovalPage, error) {
	return f(ctx, after)
}

func TestBackgroundCollectorUsesOnlyStoredGMVotes(t *testing.T) {
	c, _, payments := directRelayFixture(t, 1)
	db := store.NewMemory()
	defer db.Close()
	c.db = db
	payment := payments[0]
	var feeds [4]ApprovalSource
	ready := false
	for i := 0; i < 3; i++ {
		feeds[i] = approvalFeedFunc(func(_ context.Context, after []byte) (protocol.ApprovalPage, error) {
			if i == 2 && !ready {
				return protocol.ApprovalPage{}, nil
			}
			key := state.Key(239, payment.Certificate.QC.Fact[:])
			if bytes.Compare(key, after) <= 0 {
				return protocol.ApprovalPage{}, nil
			}
			return protocol.ApprovalPage{Next: key, Items: []protocol.ApprovedDirect{{Request: protocol.DirectRequest{Tx: payment.Tx}, Approval: protocol.DirectApproval{Summary: payment.Certificate.Summary, Vote: payment.Certificate.QC.Votes[i]}}}}, nil
		})
	}
	b := NewApprovalCollector(c, feeds)
	if err := b.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Scan(db, state.Key(state.KeyOutbox), nil, 10)
	if err != nil || len(entries) != 0 {
		t.Fatal("submitted without a quorum", err)
	}
	ready = true
	if err := b.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err = store.Scan(db, state.Key(state.KeyOutbox), nil, 10)
	if err != nil || len(entries) != 1 {
		t.Fatal("missing or duplicate submission", err)
	}
	var pending state.Outbox
	if err := db.View(func(v state.ReadView) error {
		var e error
		_, _, e = state.Load[state.Outbox](v, entries[0].Key)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.View(func(v state.ReadView) error {
		var e error
		pending, _, e = state.Load[state.Outbox](v, entries[0].Key)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	got, err := protocol.DecodeDirectPayment(pending.Certificate)
	if err != nil || got.Tx.ID() != payment.Tx.ID() || got.Certificate.Verify(c.org) != nil {
		t.Fatal("background payment invalid", err)
	}
}
