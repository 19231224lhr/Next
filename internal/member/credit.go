package member

import (
	"utxo/finality"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

func (m *Member) ApplyProof(proof finality.FactProof) error {
	verified, e := finality.Verify(m.cfg.Committee, proof)
	if e != nil {
		return e
	}
	fact := verified.Fact()
	if fact.Network != m.cfg.Organization.Network || fact.Rules != m.cfg.Schedule.IDs() {
		return protocol.ErrRule
	}
	if fact.Kind == protocol.FactOutputCreated {
		output, e := protocol.DecodeSettledOutput(fact.Payload)
		if e != nil {
			return e
		}
		if fact.Key != protocol.Hash(output.ID) || output.Output.Recipient.Network != fact.Network || fact.Revision != 1 {
			return protocol.ErrRule
		}
		return m.db.Update(func(v state.ReadView) ([]state.Change, error) {
			o := state.NewOverlay(v)
			key := state.Key(state.KeyCreation, output.ID[:])
			old, found, e := state.Load[state.Creation](o, key)
			if e != nil {
				return nil, e
			}
			if found && old.Output != output.Output {
				return nil, protocol.ErrRule
			}
			// Creation and consumption live under different keys: importing a positive
			// creation proof never erases a candidate or a consumed tombstone.
			if e = state.Put(o, key, state.Creation{Output: output.Output, Fact: fact.ID(), Source: protocol.Hash(output.Spend), Final: true}); e != nil {
				return nil, e
			}
			return o.Changes(), nil
		})
	}
	if fact.Kind != protocol.FactCredit && fact.Kind != protocol.FactWork && fact.Kind != protocol.FactCustody {
		return protocol.ErrUnsupported
	}
	receipt, e := protocol.DecodeCredit(fact.Payload)
	var custody *protocol.CustodyReceipt
	var proofBytes []byte
	if fact.Kind == protocol.FactCustody {
		parsed, err := protocol.DecodeCustody(fact.Payload)
		e = err
		receipt = parsed.Credit
		custody = &parsed
		if e == nil {
			proofBytes, e = proof.MarshalBinary()
		}
	}
	if e != nil {
		return e
	}
	if fact.Key != receipt.Key() || fact.Revision != receipt.Revision {
		return protocol.ErrRule
	}
	switch receipt.Resource.Kind {
	case protocol.ResourceFUEL, protocol.ResourcePolicy:
		if fact.Kind != protocol.FactCredit {
			return protocol.ErrRule
		}
	case protocol.ResourceExecution:
		if fact.Kind != protocol.FactWork {
			return protocol.ErrRule
		}
	case protocol.ResourceBytes:
		if fact.Kind != protocol.FactCustody || custody == nil || receipt.Paid != 0 || receipt.Returned != 0 || receipt.Remaining != 0 || receipt.Discharged != receipt.Original {
			return protocol.ErrRule
		}
	default:
		return protocol.ErrUnsupported
	}
	// A previously applied authenticated cumulative value has no write to do.
	var unchanged bool
	if err := m.db.View(func(v state.ReadView) error {
		old, found, err := state.Load[rules.CreditState](v, state.Key(state.KeyCredit, receipt.Spend[:], receipt.Resource.Encode()))
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		next := rules.CreditState{Original: receipt.Original, Paid: receipt.Paid, Discharged: receipt.Discharged, Returned: receipt.Returned, Remaining: receipt.Remaining, Revision: receipt.Revision}
		applied, _, err := rules.ApplyCredit(old, next)
		unchanged = err == nil && applied == old
		return err
	}); err != nil {
		return err
	}
	if unchanged {
		return nil
	}
	return m.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if e = state.Put(o, state.Key(state.KeyObserved, receipt.Spend[:]), true); e != nil {
			return nil, e
		}
		approval, found, e := state.Load[state.Approval](o, state.Key(state.KeyApproval, receipt.Spend[:]))
		if e != nil {
			return nil, e
		}
		if custody != nil {
			if found && custody.Effects != approval.Effects.Hash() {
				return nil, protocol.ErrAuth
			}
			// Persist the authenticated public replacement before releasing logical B.
			// Original local evidence is retained as history until the archive moves it;
			// the physical storage high-water mark remains independent of B.
			reference := state.Custody{Fact: fact.ID(), Certificate: custody.Certificate, Effects: custody.Effects, Proof: proofBytes}
			if e = state.Put(o, state.Key(state.KeyCustody, receipt.Spend[:]), reference); e != nil {
				return nil, e
			}
		}
		if !found {
			return o.Changes(), nil
		}
		if approval.Tx.Body.Config != m.cfg.Organization.Hash() {
			return nil, protocol.ErrAuth
		}
		index := -1
		for i, d := range approval.Debits {
			if d.Key == receipt.Resource {
				index = i
				break
			}
		}
		if index < 0 || approval.Debits[index].Cap != receipt.Original {
			return nil, protocol.ErrRule
		}
		debit := &approval.Debits[index]
		key := state.Key(state.KeyCredit, receipt.Spend[:], receipt.Resource.Encode())
		old, found, e := state.Load[rules.CreditState](o, key)
		if e != nil {
			return nil, e
		}
		if !found {
			old = rules.CreditState{Original: debit.Cap, Remaining: debit.Cap}
		}
		next := rules.CreditState{Original: receipt.Original, Paid: receipt.Paid, Discharged: receipt.Discharged, Returned: receipt.Returned, Remaining: receipt.Remaining, Revision: receipt.Revision}
		applied, delta, e := rules.ApplyCredit(old, next)
		if e != nil {
			return nil, e
		}
		if applied == old {
			return o.Changes(), nil
		}
		sliceKey := state.SliceKey(debit.Key, debit.Worker)
		slice, found, e := state.Load[state.Slice](o, sliceKey)
		if e != nil {
			return nil, e
		}
		if !found {
			return nil, rules.ErrMissing
		}
		slice.Reserved, e = protocol.Sub(slice.Reserved, delta)
		if e != nil {
			return nil, e
		}
		slice.Available, e = protocol.Add(slice.Available, delta)
		if e != nil {
			return nil, e
		}
		debit.Applied, e = protocol.Add(debit.Applied, delta)
		if e != nil {
			return nil, e
		}
		if e = state.Put(o, sliceKey, slice); e != nil {
			return nil, e
		}
		if e = state.Put(o, key, applied); e != nil {
			return nil, e
		}
		if e = state.Put(o, state.Key(state.KeyApproval, receipt.Spend[:]), approval); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	})
}
