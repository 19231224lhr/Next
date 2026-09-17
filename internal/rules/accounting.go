package rules

import (
	"errors"
	"utxo/protocol"
)

var ErrAccounting = errors.New("inconsistent cumulative accounting")

type CreditState struct{ Original, Paid, Discharged, Returned, Remaining, Revision uint64 }

func (c CreditState) Validate() error {
	sum, e := protocol.Add(c.Paid, c.Discharged)
	if e != nil {
		return e
	}
	sum, e = protocol.Add(sum, c.Remaining)
	if e != nil {
		return e
	}
	if sum != c.Original || c.Returned > c.Paid {
		return ErrAccounting
	}
	return nil
}
func (c CreditState) Residual() (uint64, error) {
	if e := c.Validate(); e != nil {
		return 0, e
	}
	credit, e := protocol.Add(c.Discharged, c.Returned)
	if e != nil {
		return 0, e
	}
	return protocol.Sub(c.Original, credit)
}

// ApplyCredit consumes an already authenticated fact. Callers bind its resource,
// original approval and authorization before changing the corresponding slice.
func ApplyCredit(old, next CreditState) (CreditState, uint64, error) {
	if e := old.Validate(); e != nil {
		return old, 0, e
	}
	if e := next.Validate(); e != nil {
		return old, 0, e
	}
	if old.Original != next.Original {
		return old, 0, ErrAccounting
	}
	if next.Revision < old.Revision {
		return old, 0, nil
	}
	if next.Revision == old.Revision {
		if next != old {
			return old, 0, ErrAccounting
		}
		return old, 0, nil
	}
	if next.Paid < old.Paid || next.Discharged < old.Discharged || next.Returned < old.Returned || next.Remaining > old.Remaining {
		return old, 0, ErrAccounting
	}
	a, _ := old.Residual()
	b, _ := next.Residual()
	delta, e := protocol.Sub(a, b)
	if e != nil {
		return old, 0, e
	}
	return next, delta, nil
}

type FeeAction uint8

const (
	FeeRegister FeeAction = 1
	FeeSettle   FeeAction = 2
	FeeClose    FeeAction = 3
)

type FeePlan struct{ Register, Settle, Close, Burn uint64 }
type Escrow struct {
	Maximum, Held, Rewards, Burned, Refunded uint64
	Plan                                     FeePlan
	Completed                                uint8
	Closed                                   bool
}

func NewEscrow(maximum uint64, plan FeePlan) (Escrow, error) {
	total, e := protocol.Add(plan.Register, plan.Settle)
	if e != nil {
		return Escrow{}, e
	}
	total, e = protocol.Add(total, plan.Close)
	if e != nil {
		return Escrow{}, e
	}
	if total > maximum || plan.Burn > plan.Close {
		return Escrow{}, ErrAccounting
	}
	return Escrow{Maximum: maximum, Held: maximum, Plan: plan}, nil
}
func (f Escrow) Validate() error {
	total, e := protocol.Add(f.Held, f.Rewards)
	if e != nil {
		return e
	}
	total, e = protocol.Add(total, f.Burned)
	if e != nil {
		return e
	}
	total, e = protocol.Add(total, f.Refunded)
	if e != nil {
		return e
	}
	if total != f.Maximum {
		return ErrAccounting
	}
	return nil
}
func (f Escrow) Complete(action FeeAction) (Escrow, error) {
	if e := f.Validate(); e != nil {
		return f, e
	}
	if action < FeeRegister || action > FeeClose {
		return f, protocol.ErrUnsupported
	}
	bit := uint8(1) << action
	if f.Completed&bit != 0 {
		return f, nil
	}
	if f.Closed {
		return f, ErrAccounting
	}
	original := f
	cost := f.Plan.Register
	if action == FeeSettle {
		if f.Completed&(1<<FeeRegister) == 0 {
			return f, ErrAccounting
		}
		cost = f.Plan.Settle
	}
	if action == FeeClose {
		if f.Completed&(1<<FeeSettle) == 0 {
			return f, ErrAccounting
		}
		cost = f.Plan.Close
	}
	var e error
	f.Held, e = protocol.Sub(f.Held, cost)
	if e != nil {
		return original, e
	}
	reward := cost
	if action == FeeClose {
		reward, e = protocol.Sub(cost, f.Plan.Burn)
		if e != nil {
			return original, e
		}
		f.Burned, e = protocol.Add(f.Burned, f.Plan.Burn)
		if e != nil {
			return original, e
		}
		f.Refunded = f.Held
		f.Held = 0
		f.Closed = true
	}
	f.Rewards, e = protocol.Add(f.Rewards, reward)
	if e != nil {
		return original, e
	}
	f.Completed |= bit
	if e = f.Validate(); e != nil {
		return original, e
	}
	return f, nil
}
