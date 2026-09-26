package securitymodel

import "testing"

// FUEL uses a separate bounded model: one final user input, maximum=4,
// register=1, settle=1, close=1 (burned), and at most one repair costing 1.
type feeState struct {
	input, change, refund, held, rewards, burned, refunded int
	paid, registered, settled, closed, resolved, repaired  bool
}

func feeNext(duplicateRefund bool) func(feeState) []edge[feeState] {
	return func(s feeState) (out []edge[feeState]) {
		add := func(n feeState, name string) { out = append(out, edge[feeState]{n, name}) }
		if !s.paid {
			n := s
			n.input = 0
			n.change = 6
			n.held = 4
			n.paid = true
			add(n, "reserve")
		}
		if s.paid && !s.registered {
			n := s
			n.held--
			n.rewards++
			n.registered = true
			add(n, "register")
		}
		if s.registered && !s.settled {
			n := s
			n.held--
			n.rewards++
			n.settled = true
			add(n, "settle")
		}
		if s.settled && !s.resolved {
			n := s
			n.resolved = true
			add(n, "parent")
			n.held--
			n.rewards++
			n.repaired = true
			add(n, "repair")
		}
		if s.resolved && !s.closed {
			n := s
			n.held--
			n.burned++
			n.refunded = n.held
			n.refund = n.held
			n.held = 0
			n.closed = true
			add(n, "close")
		}
		if duplicateRefund && s.closed && s.refunded > 0 {
			n := s
			n.refund += s.refunded
			add(n, "refund-again")
		}
		add(s, "duplicate")
		return out
	}
}

func checkFee(s feeState) string {
	if s.input+s.change+s.refund+s.held+s.rewards+s.burned != 10 {
		return "FUEL conservation"
	}
	if s.paid && s.held+s.rewards+s.burned+s.refunded != 4 {
		return "escrow conservation"
	}
	if s.held < 0 || s.input < 0 || s.change < 0 || s.refund < 0 {
		return "negative FUEL"
	}
	if s.closed && (!s.resolved || s.held != 0) {
		return "premature close"
	}
	return ""
}

func TestFeesBounded(t *testing.T) {
	requireExhausted(t, search(feeState{input: 10}, feeNext(false), checkFee, 100))
}

func TestFeeMutant(t *testing.T) {
	requireWitness(t, search(feeState{input: 10}, feeNext(true), checkFee, 100), "FUEL conservation")
}
