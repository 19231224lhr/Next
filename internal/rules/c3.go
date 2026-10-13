package rules

import "utxo/protocol"

type C3Stage uint8

const (
	C3Attach C3Stage = iota + 1
	C3Execute
	C3Open
	C3Fulfill
	C3Compensate
	C3Repay
)
const C3WholeTransaction uint32 = ^uint32(0)

type C3EventKey struct {
	Network, Config protocol.Hash
	Fact            protocol.SpendFactID
	OutputIndex     uint32
	Stage           C3Stage
}

// C3Escrow contains only values and can be copied into an atomic state overlay.
// The integrating ledger authenticates the certificate and event prerequisites;
// this machine enforces fee ordering, immutable terms, and once-only charging.
// It never collects the user quote or mutates an account.
type C3Escrow struct {
	Network, Config                 protocol.Hash
	Fact                            protocol.SpendFactID
	Commitment                      protocol.FeeCommitment
	Held, Rewards, Burned, Released uint64
	Completed                       uint8
	Outputs                         [protocol.MaxOutputs]uint8
}

func NewC3Escrow(network, config protocol.Hash, fact protocol.SpendFactID, c protocol.FeeCommitment) (C3Escrow, error) {
	s := C3Escrow{Network: network, Config: config, Fact: fact, Commitment: c, Held: c.Budget}
	return s, s.Validate()
}
func (s C3Escrow) EventKey(stage C3Stage, index uint32) C3EventKey {
	return C3EventKey{s.Network, s.Config, s.Fact, index, stage}
}
func c3Bit(stage C3Stage) uint8           { return 1 << stage }
func (s C3Escrow) has(stage C3Stage) bool { return s.Completed&c3Bit(stage) != 0 }

// totals derives all accounting from event masks. Each term is a disjoint part
// of the checked plan, so their sums cannot exceed its checked Maximum.
func (s C3Escrow) totals() (rewards, burned, released, held uint64, err error) {
	if err = s.Commitment.Validate(); err != nil {
		return
	}
	p, _ := s.Commitment.Plan()
	if s.Completed & ^(c3Bit(C3Attach)|c3Bit(C3Execute)) != 0 || s.has(C3Execute) && !s.has(C3Attach) {
		err = ErrAccounting
		return
	}
	if s.has(C3Attach) {
		rewards += p.AttachReward
		burned += p.Register
	}
	if s.has(C3Execute) {
		rewards += p.ExecuteReward
	}
	for i, m := range s.Outputs {
		if uint32(i) >= p.Outputs {
			if m != 0 {
				err = ErrAccounting
				return
			}
			continue
		}
		open, fulfill, comp, repay := m&c3Bit(C3Open) != 0, m&c3Bit(C3Fulfill) != 0, m&c3Bit(C3Compensate) != 0, m&c3Bit(C3Repay) != 0
		if m & ^(c3Bit(C3Open)|c3Bit(C3Fulfill)|c3Bit(C3Compensate)|c3Bit(C3Repay)) != 0 || m != 0 && !s.has(C3Attach) || fulfill && comp || comp && !open || repay && (!comp || !s.has(C3Execute)) || fulfill && !s.has(C3Execute) || s.has(C3Execute) && (!fulfill && !repay) {
			err = ErrAccounting
			return
		}
		if open {
			burned += p.Open
		}
		if fulfill || comp {
			rewards += p.ResolveReward
			if open {
				burned += p.Close
			}
		}
		if fulfill {
			released += p.Compensate + p.Repay
			if !open {
				released += p.Open + p.Close
			}
		}
		if comp {
			burned += p.Compensate
		}
		if repay {
			burned += p.Repay
		}
	}
	held = p.Maximum - rewards - burned - released
	return
}
func (s C3Escrow) Validate() error {
	if s.Network == (protocol.Hash{}) || s.Config == (protocol.Hash{}) || s.Fact == (protocol.SpendFactID{}) {
		return ErrAccounting
	}
	r, b, u, h, e := s.totals()
	if e != nil {
		return e
	}
	if s.Rewards != r || s.Burned != b || s.Released != u || s.Held != h {
		return ErrAccounting
	}
	return nil
}

// MaxFutureCost is exact for a validated state, including source execution and
// repayment after all outputs have been compensated. Call Validate first when
// reading persisted state; this query derives cost from the immutable event masks.
func (s C3Escrow) MaxFutureCost() uint64 {
	_, _, _, h, e := s.totals()
	if e != nil {
		return 0
	}
	return h
}
func (s C3Escrow) ValidateCommitment(c protocol.FeeCommitment) error {
	if c != s.Commitment {
		return ErrAccounting
	}
	return s.Validate()
}

// Apply returns the original state on failure. Execute resolves every direct
// output atomically: untouched/open outputs are fulfilled; compensated outputs
// are repaid. Explicit repeats of those output events are therefore no-ops.
func (s C3Escrow) Apply(c protocol.FeeCommitment, stage C3Stage, index uint32) (C3Escrow, error) {
	if e := s.ValidateCommitment(c); e != nil {
		return s, e
	}
	if stage < C3Attach || stage > C3Repay {
		return s, protocol.ErrUnsupported
	}
	whole := stage == C3Attach || stage == C3Execute
	if whole && index != C3WholeTransaction || !whole && index >= c.Resources.Outputs {
		return s, ErrAccounting
	}
	n := s
	bit := c3Bit(stage)
	if whole {
		if s.Completed&bit != 0 {
			return s, nil
		}
	} else {
		if s.Outputs[index]&bit != 0 {
			return s, nil
		}
	}
	if stage != C3Attach && !s.has(C3Attach) {
		return s, ErrAccounting
	}
	switch stage {
	case C3Attach:
		n.Completed |= bit
	case C3Execute:
		n.Completed |= bit
		for i := uint32(0); i < c.Resources.Outputs; i++ {
			if n.Outputs[i]&c3Bit(C3Compensate) != 0 {
				n.Outputs[i] |= c3Bit(C3Repay)
			} else {
				n.Outputs[i] |= c3Bit(C3Fulfill)
			}
		}
	case C3Open:
		if s.has(C3Execute) || s.Outputs[index] != 0 {
			return s, ErrAccounting
		}
		n.Outputs[index] |= bit
	case C3Compensate:
		if s.has(C3Execute) || s.Outputs[index] != c3Bit(C3Open) {
			return s, ErrAccounting
		}
		n.Outputs[index] |= bit
	case C3Fulfill, C3Repay:
		// These events are recorded by Execute; an unseen one is out of order or
		// conflicts with the existing output resolution.
		return s, ErrAccounting
	}
	var e error
	n.Rewards, n.Burned, n.Released, n.Held, e = n.totals()
	if e != nil {
		return s, e
	}
	return n, nil
}
