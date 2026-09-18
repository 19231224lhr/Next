package member

import (
	"utxo/finality"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

type preparedProof struct {
	fact       protocol.FinalFact
	receipt    protocol.CreditReceipt
	custody    *protocol.CustodyReceipt
	proofBytes []byte
}

// Snapshot once: the persisted custody bytes and authenticated fact must agree.
func (m *Member) prepareProof(input finality.FactProof) (preparedProof, error) {
	raw, e := input.MarshalBinary()
	if e != nil {
		return preparedProof{}, e
	}
	proof, e := finality.Decode(raw)
	if e != nil {
		return preparedProof{}, e
	}
	verified, e := finality.Verify(m.cfg.Committee, proof)
	if e != nil {
		return preparedProof{}, e
	}
	fact := verified.Fact()
	if fact.Network != m.cfg.Organization.Network || fact.Rules != m.cfg.Schedule.IDs() {
		return preparedProof{}, protocol.ErrRule
	}
	if fact.Kind == protocol.FactOutputCreated {
		return preparedProof{fact: fact}, nil
	}

	if fact.Kind != protocol.FactCredit && fact.Kind != protocol.FactWork && fact.Kind != protocol.FactCustody {
		return preparedProof{}, protocol.ErrUnsupported
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
			proofBytes = raw
		}
	}
	if e != nil {
		return preparedProof{}, e
	}
	if fact.Key != receipt.Key() || fact.Revision != receipt.Revision {
		return preparedProof{}, protocol.ErrRule
	}
	switch receipt.Resource.Kind {
	case protocol.ResourceFUEL, protocol.ResourcePolicy:
		if fact.Kind != protocol.FactCredit {
			return preparedProof{}, protocol.ErrRule
		}
	case protocol.ResourceExecution:
		if fact.Kind != protocol.FactWork {
			return preparedProof{}, protocol.ErrRule
		}
	case protocol.ResourceBytes:
		if fact.Kind != protocol.FactCustody || custody == nil || receipt.Paid != 0 || receipt.Returned != 0 || receipt.Remaining != 0 || receipt.Discharged != receipt.Original {
			return preparedProof{}, protocol.ErrRule
		}
	default:
		return preparedProof{}, protocol.ErrUnsupported
	}
	return preparedProof{fact: fact, receipt: receipt, custody: custody, proofBytes: proofBytes}, nil
}

func (m *Member) ApplyProof(proof finality.FactProof) error {
	p, e := m.prepareProof(proof)
	if e != nil {
		return e
	}
	return m.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if e := m.applyProof(o, p); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	})
}

// ApplyReceipts authenticates a partial or complete receipt set once and commits
// all resource updates and delivery progress on the same overlay.
func (m *Member) ApplyReceipts(c protocol.TXCer, proofs []finality.FactProof) (rules.ReceiptProgress, error) {
	var zero rules.ReceiptProgress
	if len(proofs) > len(c.Admission) {
		return zero, protocol.ErrRule
	}
	prepared := make([]preparedProof, 0, len(proofs))
	facts := make([]protocol.FinalFact, 0, len(proofs))
	for _, proof := range proofs {
		p, e := m.prepareProof(proof)
		if e != nil {
			return zero, e
		}
		prepared = append(prepared, p)
		facts = append(facts, p.fact)
	}
	progress, e := rules.CheckReceipts(c, facts)
	if e != nil {
		return zero, e
	}
	if len(prepared) == 0 {
		return progress, nil
	}
	e = m.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		for _, p := range prepared {
			if e := m.applyProof(o, p); e != nil {
				return nil, e
			}
		}
		if e := rules.FinishOutbox(o, c.QC.Fact, progress); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	})
	if e != nil {
		return zero, e
	}
	return progress, nil
}

func (m *Member) applyProof(o *state.Overlay, p preparedProof) error {
	fact, receipt, custody, proofBytes := p.fact, p.receipt, p.custody, p.proofBytes
	var e error
	if fact.Kind == protocol.FactOutputCreated {
		output, e := protocol.DecodeSettledOutput(fact.Payload)
		if e != nil {
			return e
		}
		if fact.Key != protocol.Hash(output.ID) || output.Output.Recipient.Network != fact.Network || fact.Revision != 1 {
			return protocol.ErrRule
		}

		key := state.Key(state.KeyCreation, output.ID[:])
		old, found, e := state.Load[state.Creation](o, key)
		if e != nil {
			return e
		}
		if found && old.Output != output.Output {
			return protocol.ErrRule
		}
		// Creation and consumption live under different keys: importing a positive
		// creation proof never erases a candidate or a consumed tombstone.
		if e = state.Put(o, key, state.Creation{Output: output.Output, Fact: fact.ID(), Source: protocol.Hash(output.Spend), Final: true}); e != nil {
			return e
		}
		return nil
	}
	if e = state.Put(o, state.Key(state.KeyObserved, receipt.Spend[:]), true); e != nil {
		return e
	}
	approval, found, e := state.Load[state.Approval](o, state.Key(state.KeyApproval, receipt.Spend[:]))
	if e != nil {
		return e
	}
	if custody != nil {
		if found && custody.Effects != approval.Effects.Hash() {
			return protocol.ErrAuth
		}
		// Persist the authenticated public replacement before releasing logical B.
		// Original local evidence is retained as history until the archive moves it;
		// the physical storage high-water mark remains independent of B.
		reference := state.Custody{Fact: fact.ID(), Certificate: custody.Certificate, Effects: custody.Effects, Proof: proofBytes}
		if e = state.Put(o, state.Key(state.KeyCustody, receipt.Spend[:]), reference); e != nil {
			return e
		}
	}
	if !found {
		return nil
	}
	if approval.Tx.Body.Config != m.cfg.Organization.Hash() {
		return protocol.ErrAuth
	}
	index := -1
	for i, d := range approval.Debits {
		if d.Key == receipt.Resource {
			index = i
			break
		}
	}
	if index < 0 || approval.Debits[index].Cap != receipt.Original {
		return protocol.ErrRule
	}
	debit := &approval.Debits[index]
	key := state.Key(state.KeyCredit, receipt.Spend[:], receipt.Resource.Encode())
	old, found, e := state.Load[rules.CreditState](o, key)
	if e != nil {
		return e
	}
	if !found {
		old = rules.CreditState{Original: debit.Cap, Remaining: debit.Cap}
	}
	next := rules.CreditState{Original: receipt.Original, Paid: receipt.Paid, Discharged: receipt.Discharged, Returned: receipt.Returned, Remaining: receipt.Remaining, Revision: receipt.Revision}
	applied, delta, e := rules.ApplyCredit(old, next)
	if e != nil {
		return e
	}
	if applied == old {
		return nil
	}
	sliceKey := state.SliceKey(debit.Key, debit.Worker)
	slice, found, e := state.Load[state.Slice](o, sliceKey)
	if e != nil {
		return e
	}
	if !found {
		return rules.ErrMissing
	}
	slice.Reserved, e = protocol.Sub(slice.Reserved, delta)
	if e != nil {
		return e
	}
	slice.Available, e = protocol.Add(slice.Available, delta)
	if e != nil {
		return e
	}
	debit.Applied, e = protocol.Add(debit.Applied, delta)
	if e != nil {
		return e
	}
	if e = state.Put(o, sliceKey, slice); e != nil {
		return e
	}
	if e = state.Put(o, key, applied); e != nil {
		return e
	}
	if e = state.Put(o, state.Key(state.KeyApproval, receipt.Spend[:]), approval); e != nil {
		return e
	}
	return nil
}
