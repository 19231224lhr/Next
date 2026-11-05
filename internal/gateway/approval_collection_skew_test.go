package gateway

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

// Regression reproducer: every candidate has three authentic signatures and
// sources retain their records and honor every requested cursor. Delayed feeds
// eventually return, so bounded common-cursor replay can safely recover votes.
func TestBackgroundCollectorRetainsSkewedQuorumAcrossOneFeedPass(t *testing.T) {
	const count = 320
	c, _, payments := directRelayFixture(t, count)
	c.db = store.NewMemory()
	defer c.db.Close()
	sort.Slice(payments, func(i, j int) bool {
		a, b := payments[i].Certificate.QC.Fact, payments[j].Certificate.QC.Fact
		return bytes.Compare(a[:], b[:]) < 0
	})
	thirdReady := false
	var sources [4]ApprovalSource
	for member := 0; member < 3; member++ {
		sources[member] = approvalFeedFunc(func(_ context.Context, after []byte) (protocol.ApprovalPage, error) {
			if member == 2 && !thirdReady {
				return protocol.ApprovalPage{}, errors.New("deliberately delayed source")
			}
			var page protocol.ApprovalPage
			for _, pay := range payments {
				key := state.Key(239, pay.Certificate.QC.Fact[:])
				if len(after) > 0 && bytes.Compare(key, after) <= 0 {
					continue
				}
				page.Items = append(page.Items, protocol.ApprovedDirect{Request: protocol.DirectRequest{Tx: pay.Tx}, Approval: protocol.DirectApproval{Summary: pay.Certificate.Summary, Vote: pay.Certificate.QC.Votes[member]}})
				page.Next = key
				if len(page.Items) == protocol.ApprovalFeedPageSize {
					break
				}
			}
			return page, nil
		})
	}
	b := NewApprovalCollector(c, sources)
	pages := count / protocol.ApprovalFeedPageSize
	for i := 0; i < pages; i++ {
		if err := b.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("after first two full feeds: authentic candidates=%d retained groups=%d", count, len(b.pending))
	thirdReady = true
	for i := 0; i < pages; i++ {
		if err := b.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := store.Scan(c.db, state.Key(state.KeyOutbox), nil, count+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != count {
		t.Fatalf("lost quorum accumulation: %d/%d outboxes after every genuine three-signature candidate was delivered; %d pending groups need another feed traversal", len(entries), count, len(b.pending))
	}
}

func TestBackgroundCollectorSplitPairsNeedThirdAuthenticVote(t *testing.T) {
	c, _, payments := directRelayFixture(t, 2)
	c.db = store.NewMemory()
	defer c.db.Close()
	f := testkit.NewFixture("direct-relay", "org", 2)
	late := false
	var sources [4]ApprovalSource
	for member := range sources {
		sources[member] = approvalFeedFunc(func(_ context.Context, after []byte) (protocol.ApprovalPage, error) {
			indices := []int{member / 2}
			if member == 2 && late {
				indices = append(indices, 0)
			}
			var items []protocol.ApprovedDirect
			for _, j := range indices {
				p := payments[j]
				items = append(items, protocol.ApprovedDirect{Request: protocol.DirectRequest{Tx: p.Tx}, Approval: protocol.DirectApproval{Summary: p.Certificate.Summary, Vote: protocol.SignSpend(p.Certificate.QC.Fact, uint16(member), f.Keys[member])}})
			}
			sort.Slice(items, func(i, j int) bool {
				a, b := items[i].Approval.Summary.Fact(), items[j].Approval.Summary.Fact()
				return bytes.Compare(a[:], b[:]) < 0
			})
			var page protocol.ApprovalPage
			for _, item := range items {
				fact := item.Approval.Summary.Fact()
				key := state.Key(239, fact[:])
				if len(after) > 0 && bytes.Compare(key, after) <= 0 {
					continue
				}
				page.Items = append(page.Items, item)
				page.Next = key
			}
			return page, nil
		})
	}
	b := NewApprovalCollector(c, sources)
	for i := 0; i < 8; i++ {
		if err := b.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := store.Scan(c.db, state.Key(state.KeyOutbox), nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("published a 2–2 partial without a three-member quorum")
	}
	late = true
	for i := 0; i < 8; i++ {
		if err := b.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	entries, err = store.Scan(c.db, state.Key(state.KeyOutbox), nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("late third authentic vote: got%d outboxes, want1", len(entries))
	}
}

func TestBackgroundCollectorRejectsMalformedAndOversizedPages(t *testing.T) {
	for _, mode := range []string{"invalid-signature", "oversized-page"} {
		t.Run(mode, func(t *testing.T) {
			c, _, payments := directRelayFixture(t, 1)
			c.db = store.NewMemory()
			defer c.db.Close()
			p := payments[0]
			repaired := false
			var sources [4]ApprovalSource
			for member := 0; member < 3; member++ {
				sources[member] = approvalFeedFunc(func(_ context.Context, after []byte) (protocol.ApprovalPage, error) {
					key := state.Key(239, p.Certificate.QC.Fact[:])
					if len(after) > 0 && bytes.Compare(key, after) <= 0 {
						return protocol.ApprovalPage{}, nil
					}
					item := protocol.ApprovedDirect{Request: protocol.DirectRequest{Tx: p.Tx}, Approval: protocol.DirectApproval{Summary: p.Certificate.Summary, Vote: p.Certificate.QC.Votes[member]}}
					if member == 0 && !repaired && mode == "invalid-signature" {
						item.Approval.Vote.Signature[0] ^= 1
					}
					page := protocol.ApprovalPage{Items: []protocol.ApprovedDirect{item}, Next: key}
					if member == 0 && !repaired && mode == "oversized-page" {
						for len(page.Items) <= protocol.ApprovalFeedPageSize {
							page.Items = append(page.Items, item)
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
				t.Fatal("published without three authenticated bounded-source votes")
			}
			repaired = true
			for i := 0; i < 6; i++ {
				if err := b.Poll(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			entries, err = store.Scan(c.db, state.Key(state.KeyOutbox), nil, 2)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("repaired feed failed to recover: %d", len(entries))
			}
		})
	}
}

func TestBackgroundCollectorLaneCursorsSparseLateAndMissingFeeds(t *testing.T) {
	const target, partial = 320, 64
	c, _, payments := directRelayFixture(t, target+partial)
	c.db = store.NewMemory()
	defer c.db.Close()
	sort.Slice(payments, func(i, j int) bool {
		a, b := payments[i].Certificate.QC.Fact, payments[j].Certificate.QC.Fact
		return bytes.Compare(a[:], b[:]) < 0
	})
	thirdReady, lateReady := false, false
	var mu sync.Mutex
	var seen [4]map[string]int
	var sources [4]ApprovalSource
	for member := range sources {
		sources[member] = approvalFeedFunc(func(_ context.Context, after []byte) (protocol.ApprovalPage, error) {
			mu.Lock()
			seen[member][string(after)]++
			mu.Unlock()
			if member == 3 || member == 2 && !thirdReady {
				return protocol.ApprovalPage{}, errors.New("source temporarily unavailable")
			}
			var page protocol.ApprovalPage
			for j, pay := range payments {
				// Every sixth record initially has only the two first GM votes.
				// Retained rows become available to the third feed later.
				if member == 2 && j%6 == 0 && !lateReady {
					continue
				}
				key := state.Key(239, pay.Certificate.QC.Fact[:])
				if len(after) > 0 && bytes.Compare(key, after) <= 0 {
					continue
				}
				page.Next = key
				page.Items = append(page.Items, protocol.ApprovedDirect{Request: protocol.DirectRequest{Tx: pay.Tx}, Approval: protocol.DirectApproval{Summary: pay.Certificate.Summary, Vote: pay.Certificate.QC.Votes[member]}})
				if len(page.Items) == protocol.ApprovalFeedPageSize {
					break
				}
			}
			return page, nil
		})
	}
	b := NewApprovalCollector(c, sources)
	poll := func() {
		t.Helper()
		var wanted [4]map[string]bool
		for member := range sources {
			seen[member] = make(map[string]int)
			wanted[member] = make(map[string]bool)
			for omitted, after := range b.after {
				if member != omitted {
					wanted[member][string(after)] = true
				}
			}
		}
		if err := b.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		for member := range sources {
			if len(seen[member]) != len(wanted[member]) {
				t.Fatalf("member%d got%d cursor requests, want%d", member, len(seen[member]), len(wanted[member]))
			}
			for cursor, count := range seen[member] {
				if count != 1 || !wanted[member][cursor] {
					t.Fatalf("unexpected or duplicate request for member%d cursor%x: %d", member, cursor, count)
				}
			}
		}
		if len(b.pending) != 0 {
			t.Fatalf("partial groups carried across polls: %d", len(b.pending))
		}
	}
	// Two sources alone may replay retained rows, but cannot advance past
	// unavailable coverage and lose those votes merely by filling a cache.
	for i := 0; i < 48; i++ {
		poll()
	}
	thirdReady = true
	// Includes enough polls for a full EOF/wrap traversal with unequal density.
	for i := 0; i < 98; i++ {
		poll()
	}
	entries, err := store.Scan(c.db, state.Key(state.KeyOutbox), nil, target+partial+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != target {
		t.Fatalf("sparse three-source coverage collected %d, want%d; two-vote records must not publish", len(entries), target)
	}
	lateReady = true
	for i := 0; i < 98; i++ {
		poll()
	}
	entries, err = store.Scan(c.db, state.Key(state.KeyOutbox), nil, target+partial+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != target+partial {
		t.Fatalf("late retained votes did not recover after EOF/wrap: %d/%d", len(entries), target+partial)
	}
}
