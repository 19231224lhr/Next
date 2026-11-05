package rules

import (
	"testing"
	"utxo/protocol"
)

func c3Commitment(outputs uint32) protocol.FeeCommitment {
	return protocol.FeeCommitment{Version: 1, Policy: protocol.Digest("c3"), Issuer: protocol.Digest("issuer"), Pool: protocol.Digest("pool"), Grant: protocol.Digest("grant"), GrantVersion: 1, Quote: 10, Sponsor: protocol.Digest("sponsor"), Revenue: protocol.Digest("revenue"), Beneficiary: protocol.Digest("committee"), Resources: protocol.FeeResources{Inputs: 1, Outputs: outputs, Work: protocol.WorkLimit{Execution: 1, Bytes: 1, Depth: 1, Ancestors: 1}}, Schedule: protocol.FeeSchedule{AttachReward: 1, ResolveReward: 1, Open: 1, Close: 1, Compensate: 1, Repay: 1}, Budget: 1 + 5*uint64(outputs)}
}
func newC3(t *testing.T, c protocol.FeeCommitment) C3Escrow {
	t.Helper()
	s, e := NewC3Escrow(protocol.Digest("net"), protocol.Digest("config"), protocol.SpendFactID(protocol.Digest("fact")), c)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func stepC3(t *testing.T, s C3Escrow, stage C3Stage, index uint32) C3Escrow {
	t.Helper()
	n, e := s.Apply(s.Commitment, stage, index)
	if e != nil {
		t.Fatal(e)
	}
	if e = n.Validate(); e != nil {
		t.Fatal(e)
	}
	return n
}
func totalsC3(t *testing.T, s C3Escrow, spent, held, released uint64) {
	t.Helper()
	if s.Rewards+s.Burned != spent || s.Held != held || s.Released != released {
		t.Fatalf("totals %+v", s)
	}
	if s.Held != s.MaxFutureCost() {
		t.Fatal("future cost mismatch")
	}
}
func TestC3SixUnitPaths(t *testing.T) {
	c := c3Commitment(1)
	s := stepC3(t, newC3(t, c), C3Attach, C3WholeTransaction)
	direct := stepC3(t, s, C3Execute, C3WholeTransaction)
	totalsC3(t, direct, 2, 0, 4)
	opened := stepC3(t, s, C3Open, 0)
	fulfilled := stepC3(t, opened, C3Execute, C3WholeTransaction)
	totalsC3(t, fulfilled, 4, 0, 2)
	compensated := stepC3(t, opened, C3Compensate, 0)
	totalsC3(t, compensated, 5, 1, 0)
	repaid := stepC3(t, compensated, C3Execute, C3WholeTransaction)
	totalsC3(t, repaid, 6, 0, 0)
	for _, stage := range []C3Stage{C3Attach, C3Execute, C3Open, C3Compensate, C3Repay} {
		idx := uint32(0)
		if stage == C3Attach || stage == C3Execute {
			idx = C3WholeTransaction
		}
		n, e := repaid.Apply(c, stage, idx)
		if e != nil || n != repaid {
			t.Fatalf("retry %v: %v", stage, e)
		}
	}
	if repaid.Rewards != 2 {
		t.Fatal("extra resolve reward")
	}
}
func TestC3PartialResolutionAndImmutablePlan(t *testing.T) {
	c := c3Commitment(2)
	c.Schedule.ExecuteReward = 3
	c.Budget += 3
	s := stepC3(t, newC3(t, c), C3Attach, C3WholeTransaction)
	s = stepC3(t, s, C3Open, 0)
	s = stepC3(t, s, C3Compensate, 0)
	totalsC3(t, s, 5, 9, 0)
	for _, event := range []struct {
		s C3Stage
		i uint32
	}{{C3Repay, 0}, {C3Fulfill, 0}, {C3Compensate, 1}, {C3Open, 2}, {C3Execute, 0}} {
		if _, e := s.Apply(c, event.s, event.i); e == nil {
			t.Fatalf("bad order %v accepted", event)
		}
	}
	bad := c
	bad.Beneficiary = protocol.Digest("attacker")
	if _, e := s.Apply(bad, C3Attach, C3WholeTransaction); e == nil {
		t.Fatal("plan changed on retry")
	}
	s = stepC3(t, s, C3Execute, C3WholeTransaction)
	totalsC3(t, s, 10, 0, 4)
	if s.Rewards != 6 {
		t.Fatal("reward ceiling")
	}
	s.Released++
	if s.Validate() == nil {
		t.Fatal("corrupt state accepted")
	}
}

func TestC3AllTwoOutputPathsConserveAndRejectCorruption(t *testing.T) {
	c := c3Commitment(2)
	// Per-output paths are unopened, open, or compensated before source execution.
	for a := 0; a < 3; a++ {
		for b := 0; b < 3; b++ {
			s := newC3(t, c)
			if _, e := s.Apply(c, C3Execute, C3WholeTransaction); e == nil {
				t.Fatal("execute before attach")
			}
			s = stepC3(t, s, C3Attach, C3WholeTransaction)
			for i, state := range []int{a, b} {
				if state > 0 {
					s = stepC3(t, s, C3Open, uint32(i))
				}
				if state > 1 {
					s = stepC3(t, s, C3Compensate, uint32(i))
				}
			}
			before := s
			s = stepC3(t, s, C3Execute, C3WholeTransaction)
			if s.Rewards+s.Burned < before.Rewards+before.Burned || s.Held != 0 || s.Rewards != 3 || s.Rewards+s.Burned+s.Released != c.Budget {
				t.Fatal("resurrected spent budget")
			}
			if _, e := s.Apply(c, C3Compensate, 0); a < 2 && e == nil {
				t.Fatal("compensation after execution")
			}
			if s.EventKey(C3Attach, C3WholeTransaction) == s.EventKey(C3Open, 0) {
				t.Fatal("event collision")
			}
			for _, corrupt := range []func(*C3Escrow){func(s *C3Escrow) { s.Held++ }, func(s *C3Escrow) { s.Rewards++ }, func(s *C3Escrow) { s.Outputs[31] = 1 }, func(s *C3Escrow) { s.Completed = 255 }} {
				bad := s
				corrupt(&bad)
				if bad.Validate() == nil {
					t.Fatal("invalid persisted state")
				}
			}
		}
	}
}
