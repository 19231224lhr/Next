package rules

import (
	"utxo/internal/state"
	"utxo/protocol"
)

// ReceiptProgress is derived from authenticated facts, never HTTP acknowledgements.
type ReceiptProgress struct{ Complete, PublicComplete bool }

// CheckReceipts binds already authenticated facts to this certificate.
func CheckReceipts(c protocol.TXCer, facts []protocol.FinalFact) (ReceiptProgress, error) {
	var result ReceiptProgress
	seen := make(map[protocol.ResourceKey]bool, len(facts))
	custody := false
	for _, fact := range facts {
		receipt, e := protocol.DecodeCredit(fact.Payload)
		if fact.Kind == protocol.FactCustody {
			r, err := protocol.DecodeCustody(fact.Payload)
			e = err
			receipt = r.Credit
			if e == nil && r.Effects != c.Effects.Hash() {
				return result, protocol.ErrAuth
			}
		}
		if e != nil {
			return result, e
		}
		expected := protocol.FactCredit
		switch receipt.Resource.Kind {
		case protocol.ResourceExecution:
			expected = protocol.FactWork
		case protocol.ResourceBytes:
			expected = protocol.FactCustody
		case protocol.ResourceFUEL, protocol.ResourcePolicy:
		default:
			return result, protocol.ErrRule
		}
		if fact.Network != c.Tx.Body.Network || fact.Rules != c.Tx.Body.Rules || fact.Kind != expected || fact.Key != receipt.Key() || fact.Revision != receipt.Revision || receipt.Spend != c.QC.Fact || seen[receipt.Resource] {
			return result, protocol.ErrAuth
		}
		matched := false
		for _, a := range c.Admission {
			if a.Key == receipt.Resource && a.Cap == receipt.Original {
				matched = true
				break
			}
		}
		if !matched {
			return result, protocol.ErrAuth
		}
		seen[receipt.Resource] = true
		terminal := receipt.Remaining == 0 && receipt.Discharged == receipt.Original && receipt.Paid == 0 && receipt.Returned == 0
		if fact.Kind == protocol.FactWork && terminal {
			result.PublicComplete = true
		}
		if fact.Kind == protocol.FactCustody {
			if !terminal {
				return result, protocol.ErrRule
			}
			custody = true
		}
	}
	result.Complete = custody && len(seen) == len(c.Admission)
	return result, nil
}

// FinishOutbox shares the caller's transaction with any credit updates.
func FinishOutbox(o *state.Overlay, id protocol.SpendFactID, p ReceiptProgress) error {
	key := state.Key(state.KeyOutbox, id[:])
	pending, found, e := state.Load[state.Outbox](o, key)
	if e != nil || !found {
		return e
	}
	if pending.Fact != id {
		return protocol.ErrAuth
	}
	if p.Complete {
		o.Delete(key)
		return nil
	}
	if p.PublicComplete && !pending.PublicComplete {
		pending.PublicComplete = true
		return state.Put(o, key, pending)
	}
	return nil
}
