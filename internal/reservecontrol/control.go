// Package reservecontrol plans additive authorization from effective member watermarks.
package reservecontrol

import "utxo/protocol"

type Policy struct{ DemandPerSecond, LeadMillis, Burst, Unit, Maximum uint64 }

// Decide returns low/target free-member watermarks and a bounded global grant delta.
// Gamma is fixed at 5/4; a member receives floor(2*global/3).
func (p Policy) Decide(current, available uint64) (low, target, delta uint64, err error) {
	if p.LeadMillis == 0 || p.Unit == 0 || p.Maximum == 0 || current > p.Maximum {
		return 0, 0, 0, protocol.ErrRule
	}
	window, err := protocol.MulDiv(p.DemandPerSecond, p.LeadMillis, 1)
	if err != nil {
		return 0, 0, 0, err
	}
	window, err = protocol.Add(window, 999)
	if err != nil {
		return 0, 0, 0, err
	}
	low, err = protocol.Add(window/1000, p.Burst)
	if err != nil {
		return 0, 0, 0, err
	}
	margin := low / 4
	if low%4 != 0 {
		margin++
	}
	target, err = protocol.Add(low, margin)
	if err != nil {
		return 0, 0, 0, err
	}
	if available >= low || current == p.Maximum {
		return low, target, 0, nil
	}
	need := target - available
	// ceil(3*need/2) is conservative even when the old grant has a fractional share.
	delta, err = protocol.MulDiv(need, 3, 1)
	if err != nil {
		return 0, 0, 0, err
	}
	delta, err = protocol.Add(delta, 1)
	if err != nil {
		return 0, 0, 0, err
	}
	delta /= 2
	delta, err = protocol.Add(delta, p.Unit-1)
	if err != nil {
		return 0, 0, 0, err
	}
	delta = delta / p.Unit * p.Unit
	return low, target, min(delta, p.Maximum-current), nil
}
