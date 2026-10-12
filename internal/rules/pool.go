package rules

import (
	"utxo/internal/state"
	"utxo/protocol"
)

// PoolRecord is public escrow, not a chain of independently redeemable promises.
type PoolRecord struct {
	Deposit protocol.PoolAction
	Exit    protocol.Hash
}

func PoolKey(id protocol.Hash) []byte { return state.Key(180, id[:]) }
func PoolEscrowKey(id protocol.Hash) []byte {
	account := protocol.Digest("INTERNAL_POOL_ESCROW_V1", id[:])
	return AccountKey(account, protocol.AssetCAL)
}

func ValidatePoolRecipient(v state.ReadView, a protocol.PoolAction, c protocol.OrgConfig) error {
	org, e := validateWalletDescriptor(v, a.Network, a.To)
	if e != nil {
		return e
	}
	if a.Kind != protocol.PoolExit && org != c.Org {
		return protocol.ErrAuth
	}
	return nil
}
func ValidatePoolDeposit(v state.ReadView, a protocol.PoolAction, c protocol.OrgConfig) error {
	if a.Kind != protocol.PoolDeposit {
		return protocol.ErrRule
	}
	if e := a.Verify(c); e != nil {
		return e
	}
	if e := ValidatePoolRecipient(v, a, c); e != nil {
		return e
	}
	creation, found, e := state.Load[state.Creation](v, DirectCreationKey(a.Input, 0))
	if e != nil {
		return e
	}
	if !found || !creation.Final || creation.Fact != a.Evidence || creation.Output.Asset != protocol.AssetCAL || creation.Output.Amount != a.Amount || creation.Output.Recipient != a.To {
		return protocol.ErrAuth
	}
	spent, _, e := state.Load[state.Spend](v, DirectSpendKey(a.Input, 0))
	if e != nil {
		return e
	}
	if spent.Consumed != (protocol.SpendFactID{}) {
		return ErrConflict
	}
	return nil
}
func VerifyPoolPublic(c protocol.PoolCertificate, network protocol.Hash, orgs map[protocol.Hash]protocol.OrgConfig) error {
	org, ok := orgs[c.Action.Config]
	if !ok || c.Action.Network != network {
		return protocol.ErrAuth
	}
	if c.Action.Kind != protocol.PoolDeposit && c.Action.Kind != protocol.PoolExit {
		return protocol.ErrUnsupported
	}
	return c.Verify(org)
}
func EvaluatePool(v state.ReadView, c protocol.PoolCertificate, network protocol.Hash, orgs map[protocol.Hash]protocol.OrgConfig) (state.Transition, error) {
	var tr state.Transition
	if e := VerifyPoolPublic(c, network, orgs); e != nil {
		return tr, e
	}
	a := c.Action
	org := orgs[a.Config]
	o := state.NewOverlay(v)
	record, found, e := state.Load[PoolRecord](o, PoolKey(a.Pool))
	if e != nil {
		return tr, e
	}
	if a.Kind == protocol.PoolDeposit {
		if found {
			if record.Deposit.ID() == a.ID() {
				return tr, nil
			}
			return tr, ErrConflict
		}
		if e = ValidatePoolDeposit(o, a, org); e != nil {
			return tr, e
		}
		if e = state.Put(o, DirectSpendKey(a.Input, 0), state.Spend{Consumed: protocol.SpendFactID(a.ID())}); e != nil {
			return tr, e
		}
		if e = state.Put(o, PoolEscrowKey(a.Pool), a.Amount); e != nil {
			return tr, e
		}
		record = PoolRecord{Deposit: a}
	} else {
		if !found {
			return tr, ErrMissing
		}
		if record.Deposit.Config != a.Config || record.Deposit.Network != a.Network || record.Deposit.Amount != a.Amount {
			return tr, protocol.ErrAuth
		}
		if record.Exit != (protocol.Hash{}) {
			if record.Exit == a.ID() {
				return tr, nil
			}
			return tr, ErrConflict
		}
		if e = ValidatePoolRecipient(o, a, org); e != nil {
			return tr, e
		}
		balance, _, err := state.Load[uint64](o, PoolEscrowKey(a.Pool))
		if err != nil {
			return tr, err
		}
		if balance != a.Amount {
			return tr, ErrAccounting
		}
		if e = state.Put(o, PoolEscrowKey(a.Pool), uint64(0)); e != nil {
			return tr, e
		}
		if _, exists, err := state.Load[state.Creation](o, DirectCreationKey(a.OutputID(), 0)); err != nil {
			return tr, err
		} else if exists {
			return tr, ErrConflict
		}
		created := state.Creation{Output: protocol.Output{Asset: protocol.AssetCAL, Amount: a.Amount, Recipient: a.To}, Fact: a.CreationFact(), Final: true}
		if e = state.Put(o, DirectCreationKey(a.OutputID(), 0), created); e != nil {
			return tr, e
		}
		record.Exit = a.ID()
	}
	if e = state.Put(o, PoolKey(a.Pool), record); e != nil {
		return tr, e
	}
	tr.Changes = o.Changes()
	return tr, nil
}

// ObservePool applies an already authenticated successful public block entry.
// Followers may not possess another organization's funding UTXO. The block's
// finality, verified by PrepareBlock's caller, supplies the public execution proof.
func ObservePool(o *state.Overlay, c protocol.PoolCertificate, network protocol.Hash, orgs map[protocol.Hash]protocol.OrgConfig) error {
	if e := VerifyPoolPublic(c, network, orgs); e != nil {
		return e
	}
	a := c.Action
	record, found, e := state.Load[PoolRecord](o, PoolKey(a.Pool))
	if e != nil {
		return e
	}
	if a.Kind == protocol.PoolDeposit {
		if found {
			if record.Deposit.ID() != a.ID() {
				return ErrConflict
			}
			return nil
		}
		record = PoolRecord{Deposit: a}
		if e = state.Put(o, DirectSpendKey(a.Input, 0), state.Spend{Consumed: protocol.SpendFactID(a.ID())}); e != nil {
			return e
		}
	} else {
		if !found || record.Deposit.Config != a.Config || record.Deposit.Amount != a.Amount {
			return ErrMissing
		}
		if record.Exit != (protocol.Hash{}) && record.Exit != a.ID() {
			return ErrConflict
		}
		record.Exit = a.ID()
		if e = state.Put(o, DirectCreationKey(a.OutputID(), 0), state.Creation{Output: protocol.Output{Asset: protocol.AssetCAL, Amount: a.Amount, Recipient: a.To}, Fact: a.CreationFact(), Final: true}); e != nil {
			return e
		}
	}
	return state.Put(o, PoolKey(a.Pool), record)
}
