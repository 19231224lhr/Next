package gateway

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

// A syntactically valid forward cursor is not evidence of useful progress.
// Whichever GM is faulty, its cursor must not pin the three honest feeds.
func TestBackgroundCollectorTinyForwardCursorCannotPinHonestQuorum(t *testing.T) {
	for bad := 0; bad < 4; bad++ {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			const count = 80
			c, _, payments := directRelayFixture(t, count)
			c.db = store.NewMemory()
			defer c.db.Close()
			f := testkit.NewFixture("direct-relay", "org", 2)
			sort.Slice(payments, func(i, j int) bool {
				a, b := payments[i].Certificate.QC.Fact, payments[j].Certificate.QC.Fact
				return bytes.Compare(a[:], b[:]) < 0
			})
			var mu sync.Mutex
			calls := map[string]int{}
			var sources [4]ApprovalSource
			for member := range sources {
				sources[member] = approvalFeedFunc(func(_ context.Context, after []byte) (protocol.ApprovalPage, error) {
					mu.Lock()
					calls[fmt.Sprintf("%d:%x", member, after)]++
					mu.Unlock()
					if member == bad {
						next := bytes.Clone(after)
						if len(next) == 0 {
							next = state.Key(239, make([]byte, 32))
						}
						for j := len(next) - 1; j >= len(state.Key(239)); j-- {
							next[j]++
							if next[j] != 0 {
								break
							}
						}
						return protocol.ApprovalPage{Next: next}, nil
					}
					var page protocol.ApprovalPage
					for _, p := range payments {
						key := state.Key(239, p.Certificate.QC.Fact[:])
						if bytes.Compare(key, after) <= 0 {
							continue
						}
						page.Items = append(page.Items, protocol.ApprovedDirect{Request: protocol.DirectRequest{Tx: p.Tx}, Approval: protocol.DirectApproval{Summary: p.Certificate.Summary, Vote: protocol.SignSpend(p.Certificate.QC.Fact, uint16(member), f.Keys[member])}})
						page.Next = key
						if len(page.Items) == protocol.ApprovalFeedPageSize {
							break
						}
					}
					return page, nil
				})
			}
			b := NewApprovalCollector(c, sources)
			for poll := 0; poll < 12; poll++ {
				calls = map[string]int{}
				if err := b.Poll(context.Background()); err != nil {
					t.Fatal(err)
				}
				if len(calls) > 12 {
					t.Fatalf("unbounded poll: %d requests", len(calls))
				}
				if poll == 0 && len(calls) != 4 {
					t.Fatalf("aligned lanes sent %d requests, want 4", len(calls))
				}
				for key, count := range calls {
					if count != 1 {
						t.Fatalf("duplicate source/cursor request %s: %d", key, count)
					}
				}
			}
			entries, err := store.Scan(c.db, state.Key(state.KeyOutbox), nil, count+1)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != count {
				t.Fatalf("bad member %d pinned honest quorum: %d/%d outboxes", bad, len(entries), count)
			}
		})
	}
}
