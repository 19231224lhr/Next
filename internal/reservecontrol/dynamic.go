package reservecontrol

import (
	"math/bits"
	"sort"
	"time"
	"utxo/protocol"
)

// CALTarget uses the third-largest remaining GM allowance (3-of-4), never
// the sum of overlapping allowances. B is cumulative backing; Spent is net paid.
func CALTarget(current, reserved, spent, demand uint64, free [4]uint64) (target, available uint64, err error) {
	ceiling, err := protocol.GrantShare(current)
	if err != nil {
		return 0, 0, err
	}
	for _, a := range free {
		if a > ceiling {
			return 0, 0, protocol.ErrRule
		}
	}
	sort.Slice(free[:], func(i, j int) bool { return free[i] > free[j] })
	available = free[2]
	public, err := protocol.Add(reserved, spent)
	if err != nil {
		return 0, 0, err
	}
	public, err = protocol.Add(public, demand)
	if err != nil {
		return 0, 0, err
	}
	target = max(current, public)
	if demand > available {
		required, err := protocol.Add(ceiling, demand-available)
		if err != nil {
			return 0, 0, err
		}
		backing, err := ceilMulDiv(required, 3, 2)
		if err != nil {
			return 0, 0, err
		}
		target = max(target, backing)
	}
	return target, available, nil
}

func ceilMulDiv(a, b, d uint64) (uint64, error) {
	hi, lo := bits.Mul64(a, b)
	if d == 0 || hi >= d {
		return 0, protocol.ErrAmount
	}
	q, r := bits.Div64(hi, lo, d)
	if r != 0 {
		return protocol.Add(q, 1)
	}
	return q, nil
}

type demandSample struct {
	at      time.Time
	amount  uint64
	elapsed time.Duration
}
type DemandHistory struct {
	Window  time.Duration
	samples []demandSample
}

func (h *DemandHistory) Observe(at time.Time, amount uint64, elapsed time.Duration) error {
	if elapsed <= 0 {
		return protocol.ErrRule
	}
	h.samples = append(h.samples, demandSample{at, amount, elapsed})
	// Bounded background statistics; no transaction history scan or hot-path lock.
	if len(h.samples) > 4096 {
		copy(h.samples, h.samples[len(h.samples)-4096:])
		h.samples = h.samples[:4096]
	}
	return nil
}
func (h *DemandHistory) Forecast(now time.Time, horizon time.Duration) (uint64, error) {
	if horizon <= 0 {
		return 0, protocol.ErrRule
	}
	window := h.Window
	if window <= 0 {
		window = time.Minute
	}
	values := make([]uint64, 0, len(h.samples))
	var current uint64
	for _, s := range h.samples {
		if s.at.After(now) || now.Sub(s.at) >= window {
			continue
		}
		n, e := ceilMulDiv(s.amount, uint64(horizon), uint64(s.elapsed))
		if e != nil {
			return 0, e
		}
		values = append(values, n)
		current = n
	}
	if len(values) == 0 {
		return 0, nil
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return max(current, values[(95*len(values)+99)/100-1]), nil
}

type PublicState struct{ Grant, Reserved, Spent uint64 }
type MemberState struct {
	Valid                                     bool
	Grant, Available, ApprovedCAL, LargestCAL uint64
	Slices                                    []uint64
}
type Increase struct{ Previous, Amount uint64 }
type counterSample struct {
	at     time.Time
	amount uint64
}

// Controller is driven by one background goroutine. A pending increase is
// retried unchanged until a public update and three GM applications are seen.
type Controller struct {
	Interval, Lead             time.Duration
	Minimum, Demand, Available uint64
	History                    DemandHistory
	previous                   [4]counterSample
	pending                    *Increase
	began                      time.Time
}

func (c *Controller) Step(now time.Time, p PublicState, ms [4]MemberState) (*Increase, error) {
	if c.Interval <= 0 || c.Lead <= 0 || p.Grant == 0 {
		return nil, protocol.ErrRule
	}
	var free [4]uint64
	valid, applied, currentMembers := 0, 0, 0
	var rate, largest uint64
	for i, m := range ms {
		if !m.Valid {
			continue
		}
		valid++
		largest = max(largest, m.LargestCAL)
		prev := c.previous[i]
		if !prev.at.IsZero() && now.After(prev.at) && m.ApprovedCAL >= prev.amount {
			r, e := ceilMulDiv(m.ApprovedCAL-prev.amount, uint64(time.Second), uint64(now.Sub(prev.at)))
			if e != nil {
				return nil, e
			}
			rate = max(rate, r) // Parallel GM votes represent the same traffic, not four payments.
		}
		c.previous[i] = counterSample{now, m.ApprovedCAL}
		if m.Grant == p.Grant {
			currentMembers++
			free[i] = m.Available
		}
		if c.pending != nil && m.Grant >= c.pending.Previous+c.pending.Amount {
			applied++
		}
	}
	if valid < 3 {
		if c.pending != nil {
			out := *c.pending
			return &out, nil
		}
		return nil, nil
	}
	if e := c.History.Observe(now, rate, time.Second); e != nil {
		return nil, e
	}
	if c.pending != nil {
		if p.Grant < c.pending.Previous+c.pending.Amount || applied < 3 {
			out := *c.pending
			return &out, nil
		}
		c.Lead = max(c.Lead, now.Sub(c.began))
		c.pending = nil
	}
	if currentMembers < 3 {
		return nil, nil
	}
	if c.Lead > time.Duration(1<<63-1)-c.Interval {
		return nil, protocol.ErrAmount
	}
	demand, e := c.History.Forecast(now, c.Lead+c.Interval)
	if e != nil {
		return nil, e
	}
	c.Demand = max(demand, c.Minimum, largest)
	target, available, e := CALTarget(p.Grant, p.Reserved, p.Spent, c.Demand, free)
	if e != nil {
		return nil, e
	}
	c.Available = available
	// Hash-selected workers cannot borrow each other's free quota. Add a
	// conservative guard for a forecast share and at least one largest request
	// per worker of a signing quorum, without altering quota allocation rules.
	workerTargets := make([]uint64, 0, 4)
	ceiling, _ := protocol.GrantShare(p.Grant)
memberTargets:
	for _, m := range ms {
		if !m.Valid || m.Grant != p.Grant {
			continue
		}
		memberTarget := p.Grant
		if len(m.Slices) > 1 {
			workers := uint64(len(m.Slices))
			perWorker, e := ceilMulDiv(c.Demand, 1, workers)
			if e != nil {
				return nil, e
			}
			perWorker = max(perWorker, largest)
			var gap uint64
			for _, a := range m.Slices {
				if a < perWorker {
					gap = max(gap, perWorker-a)
				}
			}
			if gap > 0 {
				delta, e := protocol.MulDiv(gap, workers, 1)
				if e != nil {
					continue memberTargets
				}
				share, e := protocol.Add(ceiling, delta)
				if e != nil {
					continue memberTargets
				}
				memberTarget, e = ceilMulDiv(share, 3, 2)
				if e != nil {
					continue memberTargets
				}
			}
		}
		workerTargets = append(workerTargets, memberTarget)
	}
	if len(workerTargets) < 3 {
		return nil, protocol.ErrAmount
	}
	sort.Slice(workerTargets, func(i, j int) bool { return workerTargets[i] < workerTargets[j] })
	target = max(target, workerTargets[2])
	if target == p.Grant {
		return nil, nil
	}
	c.pending = &Increase{Previous: p.Grant, Amount: target - p.Grant}
	c.began = now
	out := *c.pending
	return &out, nil
}
