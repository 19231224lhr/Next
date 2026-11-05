package member

import (
	"context"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

type poolLocal struct {
	Head    protocol.PoolAction
	Pending *protocol.PoolAction
}

func poolLocalKey(id protocol.Hash) []byte    { return state.Key(161, id[:]) }
func poolApprovalKey(id protocol.Hash) []byte { return state.Key(181, id[:]) }

// ApprovePool persists the candidate before signing. Transfer and exit compete
// for the very same predecessor, so two quorums cannot certify both.
func (m *Member) ApprovePool(ctx context.Context, r protocol.PoolRequest) (protocol.PoolVote, error) {
	var vote protocol.PoolVote
	if !m.cfg.EnableInternalPools {
		return vote, protocol.ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return vote, err
	}
	a := r.Action
	c := m.cfg.Organization
	if err := a.Verify(c); err != nil {
		return vote, err
	}
	if a.Kind == protocol.PoolDeposit {
		if r.Parent != nil {
			return vote, protocol.ErrRule
		}
	} else {
		if r.Parent == nil {
			return vote, protocol.ErrAuth
		}
		var trusted bool
		if err := m.db.View(func(v state.ReadView) error {
			var e error
			trusted, e = poolParentArchived(v, *r.Parent)
			return e
		}); err != nil {
			return vote, err
		}
		if !trusted && r.Parent.Verify(c) != nil {
			return vote, protocol.ErrAuth
		}
		p := r.Parent.Action
		if p.Kind == protocol.PoolExit || p.Pool != a.Pool || p.Amount != a.Amount || p.Sequence+1 != a.Sequence || p.ID() != a.Previous || p.To.Owner != a.From {
			return vote, protocol.ErrAuth
		}
	}
	err := m.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if a.Kind == protocol.PoolDeposit {
			spent, _, err := state.Load[state.Spend](o, rules.DirectSpendKey(a.Input, 0))
			if err != nil {
				return nil, err
			}
			if spent.Consumed != (protocol.SpendFactID{}) && spent.Consumed != protocol.SpendFactID(a.ID()) {
				return nil, rules.ErrConflict
			}
		}
		// Keep signed proposals even after a newer QC supersedes a minority
		// vote. Retrying the original authorized request reconstructs that vote.
		if _, found, e := state.Load[protocol.PoolAction](o, poolApprovalKey(a.ID())); e != nil {
			return nil, e
		} else if found {
			return nil, nil
		}
		finish := func() ([]state.Change, error) {
			if e := state.Put(o, poolApprovalKey(a.ID()), a); e != nil {
				return nil, e
			}
			return o.Changes(), nil
		}
		if e := rules.ValidatePoolRecipient(o, a, c); e != nil {
			return nil, e
		}
		if a.Kind == protocol.PoolDeposit {
			// Use exactly the lock read by ApproveDirect: entering a pool and issuing
			// an ordinary payment certificate cannot both obtain three honest votes.
			if rec, found, e := state.Load[rules.PoolRecord](o, rules.PoolKey(a.Pool)); e != nil {
				return nil, e
			} else if found {
				if rec.Deposit.ID() != a.ID() || rec.Exit != (protocol.Hash{}) {
					return nil, rules.ErrConflict
				}
				return finish()
			}
			if e := rules.ValidatePoolDeposit(o, a, c); e != nil {
				return nil, e
			}
			key := rules.DirectSpendKey(a.Input, 0)
			s, _, e := state.Load[state.Spend](o, key)
			if e != nil {
				return nil, e
			}
			fact := protocol.SpendFactID(a.ID())
			if s.Candidate != (protocol.SpendFactID{}) && s.Candidate != fact {
				return nil, rules.ErrConflict
			}
			s.Candidate = fact
			if e = state.Put(o, key, s); e != nil {
				return nil, e
			}
			return finish()
		}
		rec, found, e := state.Load[rules.PoolRecord](o, rules.PoolKey(a.Pool))
		if e != nil {
			return nil, e
		}
		if !found {
			return nil, rules.ErrMissing
		}
		if rec.Exit != (protocol.Hash{}) || rec.Deposit.Config != a.Config || rec.Deposit.Amount != a.Amount {
			return nil, rules.ErrConflict
		}
		p := r.Parent.Action
		if p.Kind == protocol.PoolDeposit && p.ID() != rec.Deposit.ID() {
			return nil, protocol.ErrAuth
		}
		local, found, e := state.Load[poolLocal](o, poolLocalKey(a.Pool))
		if e != nil {
			return nil, e
		}
		if !found {
			local.Head = rec.Deposit
		}
		if p.Sequence < local.Head.Sequence || p.Sequence == local.Head.Sequence && p.ID() != local.Head.ID() {
			return nil, rules.ErrConflict
		}
		if p.Sequence > local.Head.Sequence {
			local.Head = p
			if local.Pending != nil && local.Pending.Sequence <= p.Sequence {
				local.Pending = nil
			}
		}
		if local.Pending != nil && local.Pending.ID() != a.ID() {
			return nil, rules.ErrConflict
		}
		local.Pending = &a
		if e = state.Put(o, poolLocalKey(a.Pool), local); e != nil {
			return nil, e
		}
		return finish()
	})
	if err != nil {
		return vote, err
	}
	return protocol.SignPool(a, m.cfg.Index, m.cfg.Key), nil
}
