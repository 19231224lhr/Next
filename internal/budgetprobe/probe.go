// Package budgetprobe exposes opt-in, read-only measurements for fresh E2 labs.
package budgetprobe

import (
	"encoding/json"
	"net/http"
	"time"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type Resource struct {
	Key    protocol.ResourceKey
	Grant  uint64
	Slices []state.Slice `json:",omitempty"`
	Usage  rules.PublicUsage
}
type DebitTotal struct {
	Key                         protocol.ResourceKey
	Charged, Released, NetSpent uint64
}
type Detail struct {
	Approvals, Settled, Closed, Payments, Open, Fulfilled, Repaired uint64
	OldestDeadline                                                  int64
	Debits                                                          []DebitTotal `json:",omitempty"`
	Fee                                                             rules.Escrow
}
type Snapshot struct {
	UnixNS    int64
	ReadMS    float64
	CAL, FUEL uint64
	Resources []Resource
	Limited   [6]uint64
	Detail    *Detail                 `json:",omitempty"`
	Oldest    *rules.DirectObligation `json:",omitempty"`
}

// Detailed reads are deliberately explicit; regular samples read only fixed
// grant/slice/account keys. The caller records read cost and sampling errors.
func Read(db store.Store, grants []state.Grant, org protocol.Hash, workers uint32, details bool) (s Snapshot, err error) {
	start := time.Now()
	err = db.View(func(v state.ReadView) error {
		var e error
		s.CAL, _, e = state.Load[uint64](v, rules.AccountKey(org, protocol.AssetCAL))
		if e != nil {
			return e
		}
		s.FUEL, _, e = state.Load[uint64](v, rules.AccountKey(org, protocol.AssetFUEL))
		if e != nil {
			return e
		}
		for _, g := range grants {
			current, found, err := state.Load[state.Grant](v, state.Key(state.KeyGrant, g.Key.Encode()))
			if err != nil {
				return err
			}
			if !found {
				return rules.ErrMissing
			}
			r := Resource{Key: g.Key, Grant: current.Amount}
			r.Usage, _, e = state.Load[rules.PublicUsage](v, state.Key(state.KeyUsage, g.Key.Encode()))
			if e != nil {
				return e
			}
			for w := uint32(0); w < workers; w++ {
				x, found, e := state.Load[state.Slice](v, state.SliceKey(g.Key, w))
				if e != nil {
					return e
				}
				if found {
					r.Slices = append(r.Slices, x)
				}
			}
			s.Resources = append(s.Resources, r)
		}
		if workers == 0 {
			// Only active deadline indexes, never a growing history scan. Include
			// obligations waiting for their H+1 anchor (Deadline is then zero).
			for _, prefix := range [][]byte{rules.DirectAnchorKey(0, protocol.OutputID{})[:3], rules.DirectDueKey(0, protocol.OutputID{})[:3]} {
				if e = scan(v, prefix, func(raw []byte) error {
					var q rules.DirectObligation
					if e := json.Unmarshal(raw, &q); e != nil {
						return e
					}
					o, found, e := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(q.Output))
					if e != nil {
						return e
					}
					if found && o.Status == rules.DirectOpen && (s.Oldest == nil || o.AnchorHeight < s.Oldest.AnchorHeight) {
						s.Oldest = &o
					}
					return nil
				}); e != nil {
					return e
				}
			}
		}
		if !details {
			return nil
		}
		s.Detail = &Detail{}
		if workers > 0 {
			totals := map[protocol.ResourceKey]*DebitTotal{}
			e = scan(v, state.Key(state.KeyApproval), func(raw []byte) error {
				var a state.Approval
				if e := json.Unmarshal(raw, &a); e != nil {
					return e
				}
				p, _, e := state.Load[member.LocalProgress](v, member.ProgressKey(a.Fact))
				if e != nil {
					return e
				}
				applied, e := member.AppliedDebits(v, a)
				if e != nil {
					return e
				}
				s.Detail.Approvals++
				if p.Settled {
					s.Detail.Settled++
				}
				if p.Fee.Closed {
					s.Detail.Closed++
				}
				for i, d := range a.Debits {
					t := totals[d.Key]
					if t == nil {
						t = &DebitTotal{Key: d.Key}
						totals[d.Key] = t
					}
					t.Charged += d.Cap
					t.Released += applied[i]
					if d.Key.Kind == protocol.ResourceCAL {
						t.NetSpent += p.Paid
					}
					if d.Key.Kind == protocol.ResourceFUEL || d.Key.Kind == protocol.ResourcePolicy {
						t.NetSpent += p.Fee.Rewards + p.Fee.Burned
					}
				}
				return nil
			})
			if e != nil {
				return e
			}
			for _, g := range grants {
				if t := totals[g.Key]; t != nil {
					s.Detail.Debits = append(s.Detail.Debits, *t)
				}
			}
			return nil
		}
		// Key constructors supply the three-byte typed prefix; no wire IDs copied.
		e = scan(v, rules.DirectObligationKey(protocol.OutputID{})[:3], func(raw []byte) error {
			var o rules.DirectObligation
			if e := json.Unmarshal(raw, &o); e != nil {
				return e
			}
			switch o.Status {
			case rules.DirectOpen:
				s.Detail.Open++
				if o.Deadline > 0 && (s.Detail.OldestDeadline == 0 || o.Deadline < s.Detail.OldestDeadline) {
					s.Detail.OldestDeadline = o.Deadline
				}
			case rules.DirectFulfilled:
				s.Detail.Fulfilled++
			case rules.DirectRepaired:
				s.Detail.Repaired++
			}
			return nil
		})
		if e != nil {
			return e
		}
		return scan(v, state.Key(103), func(raw []byte) error {
			var p rules.DirectPaymentState
			if e := json.Unmarshal(raw, &p); e != nil {
				return e
			}
			s.Detail.Payments++
			if p.Fee.Closed {
				s.Detail.Closed++
			}
			f := &s.Detail.Fee
			f.Maximum += p.Fee.Maximum
			f.Held += p.Fee.Held
			f.Rewards += p.Fee.Rewards
			f.Burned += p.Fee.Burned
			f.Refunded += p.Fee.Refunded
			return nil
		})
	})
	s.UnixNS = time.Now().UnixNano()
	s.ReadMS = float64(time.Since(start)) / 1e6
	return
}

func scan(v state.ReadView, prefix []byte, visit func([]byte) error) error {
	// E2 has at most 12,000 payments. A single consistent read avoids repeatedly
	// scanning a growing memory store for each page. Reject larger fixtures.
	x, e := v.(state.ScanView).Scan(prefix, nil, 100001)
	if e != nil {
		return e
	}
	if len(x) > 100000 {
		return protocol.ErrRule
	}
	for _, entry := range x {
		if e = visit(entry.Value); e != nil {
			return e
		}
	}
	return nil
}

func Handler(db store.Store, grants []state.Grant, org protocol.OrgConfig, workers uint32, limited func() [6]uint64) http.Handler {
	var own []state.Grant
	for _, g := range grants {
		if g.Organization == org.Hash() {
			own = append(own, g)
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, e := Read(db, own, org.Org, workers, r.URL.Query().Get("detail") == "1")
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		if limited != nil {
			s.Limited = limited()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s)
	})
}
