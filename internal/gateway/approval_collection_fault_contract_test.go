package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestBackgroundCollectorMergesHealthyLaneAfterSourceTimeout(t *testing.T) {
	for bad := 0; bad < 4; bad++ {
		t.Run(fmt.Sprintf("source%d", bad), func(t *testing.T) {
			c, _, payments := directRelayFixture(t, 1)
			c.db = store.NewMemory()
			defer c.db.Close()
			p := payments[0]
			f := testkit.NewFixture("direct-relay", "org", 1)
			var sources [4]ApprovalSource
			for member := range sources {
				sources[member] = approvalFeedFunc(func(ctx context.Context, after []byte) (protocol.ApprovalPage, error) {
					if member == bad {
						<-ctx.Done()
						return protocol.ApprovalPage{}, ctx.Err()
					}
					key := state.Key(239, p.Certificate.QC.Fact[:])
					if len(after) > 0 && bytes.Compare(key, after) <= 0 {
						return protocol.ApprovalPage{}, nil
					}
					return protocol.ApprovalPage{Next: key, Items: []protocol.ApprovedDirect{{Request: protocol.DirectRequest{Tx: p.Tx}, Approval: protocol.DirectApproval{Summary: p.Certificate.Summary, Vote: protocol.SignSpend(p.Certificate.QC.Fact, uint16(member), f.Keys[member])}}}}, nil
				})
			}
			b := NewApprovalCollector(c, sources)
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			err := b.Poll(ctx)
			if err != nil && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			entries, err := store.Scan(c.db, state.Key(state.KeyOutbox), nil, 2)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatal("available honest triple was discarded when another source timed out")
			}
		})
	}
}

func TestBackgroundCollectorFaultMetadataDoesNotCreateThirdSignature(t *testing.T) {
	for _, mode := range []string{"empty-forward-cursor", "wrong-member", "invalid-signature", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			c, _, payments := directRelayFixture(t, 1)
			c.db = store.NewMemory()
			defer c.db.Close()
			p := payments[0]
			f := testkit.NewFixture("direct-relay", "org", 1)
			var sources [4]ApprovalSource
			for member := range sources {
				sources[member] = approvalFeedFunc(func(_ context.Context, after []byte) (protocol.ApprovalPage, error) {
					if member == 3 {
						return protocol.ApprovalPage{}, nil
					}
					key := state.Key(239, p.Certificate.QC.Fact[:])
					if len(after) > 0 && bytes.Compare(key, after) <= 0 {
						return protocol.ApprovalPage{}, nil
					}
					item := protocol.ApprovedDirect{Request: protocol.DirectRequest{Tx: p.Tx}, Approval: protocol.DirectApproval{Summary: p.Certificate.Summary, Vote: protocol.SignSpend(p.Certificate.QC.Fact, uint16(member), f.Keys[member])}}
					page := protocol.ApprovalPage{Next: key, Items: []protocol.ApprovedDirect{item}}
					if member == 2 {
						switch mode {
						case "empty-forward-cursor":
							page.Items = nil
						case "wrong-member":
							page.Items[0].Approval.Vote = protocol.SignSpend(p.Certificate.QC.Fact, 0, f.Keys[0])
						case "invalid-signature":
							page.Items[0].Approval.Vote.Signature[0] ^= 1
						case "oversized":
							for len(page.Items) <= protocol.ApprovalFeedPageSize {
								page.Items = append(page.Items, item)
							}
						}
					}
					return page, nil
				})
			}
			b := NewApprovalCollector(c, sources)
			for i := 0; i < 6; i++ {
				if err := b.Poll(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			entries, err := store.Scan(c.db, state.Key(state.KeyOutbox), nil, 2)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatal("fault metadata supplied an unauthenticated third vote")
			}
		})
	}
}
