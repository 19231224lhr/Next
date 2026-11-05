package member

import (
	"context"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

func poolCertificateKey(id protocol.Hash) []byte { return state.Key(182, id[:]) }

// poolParentArchived recognizes only the complete proof installed after
// verification by this member. The action ID alone omits the owner signature.
func poolParentArchived(v state.ReadView, parent protocol.PoolCertificate) (bool, error) {
	archived, found, err := state.Load[protocol.PoolCertificate](v, poolCertificateKey(parent.Action.ID()))
	if err != nil || !found || len(archived.Votes) < 3 || len(archived.Votes) > 4 || len(archived.Votes) != len(parent.Votes) || archived.Action != parent.Action {
		return false, err
	}
	for i, vote := range archived.Votes {
		if vote != parent.Votes[i] {
			return false, nil
		}
	}
	return true, nil
}

// InstallPool stores a complete QC before acknowledging it. A deposit QC is
// availability evidence only: the verified public block creates PoolRecord.
// A transfer QC is a certified state anchor, including for a minority voter.
func (m *Member) InstallPool(ctx context.Context, c protocol.PoolCertificate) error {
	if !m.cfg.EnableInternalPools {
		return protocol.ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.Verify(m.cfg.Organization); err != nil {
		return err
	}
	a := c.Action
	return m.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if a.Kind != protocol.PoolDeposit {
			rec, found, err := state.Load[rules.PoolRecord](o, rules.PoolKey(a.Pool))
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, rules.ErrMissing
			}
			if rec.Deposit.Config != a.Config || rec.Deposit.Network != a.Network || rec.Deposit.Amount != a.Amount {
				return nil, protocol.ErrAuth
			}
			local, found, err := state.Load[poolLocal](o, poolLocalKey(a.Pool))
			if err != nil {
				return nil, err
			}
			if !found {
				local.Head = rec.Deposit
			}
			if local.Head.Sequence == a.Sequence && local.Head.ID() != a.ID() {
				return nil, rules.ErrConflict
			}
			// Historical QCs remain retrievable, but never roll back certified
			// state or reopen an exit already finalized in a public block.
			if rec.Exit == (protocol.Hash{}) && a.Sequence > local.Head.Sequence {
				local.Head = a
				if local.Pending != nil && local.Pending.Sequence <= a.Sequence {
					local.Pending = nil
				}
				if err = state.Put(o, poolLocalKey(a.Pool), local); err != nil {
					return nil, err
				}
			}
		}
		if err := state.Put(o, poolCertificateKey(a.ID()), c); err != nil {
			return nil, err
		}
		return o.Changes(), nil
	})
}

// PoolCertificate returns the archived full QC, never a reconstructed vote.
func (m *Member) PoolCertificate(ctx context.Context, id protocol.Hash) (protocol.PoolCertificate, error) {
	var c protocol.PoolCertificate
	if !m.cfg.EnableInternalPools {
		return c, protocol.ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return c, err
	}
	err := m.db.View(func(v state.ReadView) error {
		var found bool
		var err error
		c, found, err = state.Load[protocol.PoolCertificate](v, poolCertificateKey(id))
		if err != nil {
			return err
		}
		if !found {
			return state.ErrNotFound
		}
		if c.Action.ID() != id {
			return protocol.ErrAuth
		}
		return c.Verify(m.cfg.Organization)
	})
	return c, err
}
