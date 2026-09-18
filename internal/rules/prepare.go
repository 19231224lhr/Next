package rules

import (
	"bytes"
	"errors"
	"utxo/internal/state"
	"utxo/protocol"
)

var ErrConflict = errors.New("input already locked or consumed")
var ErrLimited = errors.New("resource quota insufficient")
var ErrMissing = errors.New("required input evidence missing")

type Schedule struct {
	Fee                                            FeePlan
	BaseExecution, InputExecution, OutputExecution uint64
	BaseBytes                                      uint64
	MaxDepth, MaxAncestors                         uint32
}

func DefaultSchedule() Schedule {
	return Schedule{Fee: FeePlan{Register: 20, Settle: 40, Close: 34, Burn: 10}, BaseExecution: 10, InputExecution: 5, OutputExecution: 2, BaseBytes: 2048, MaxDepth: 64, MaxAncestors: 256}
}
func (s Schedule) IDs() protocol.RuleIDs {
	f := new(protocol.Encoder)
	f.U64(s.Fee.Register)
	f.U64(s.Fee.Settle)
	f.U64(s.Fee.Close)
	f.U64(s.Fee.Burn)
	w := new(protocol.Encoder)
	w.U64(s.BaseExecution)
	w.U64(s.InputExecution)
	w.U64(s.OutputExecution)
	w.U64(s.BaseBytes)
	w.U32(s.MaxDepth)
	w.U32(s.MaxAncestors)
	return protocol.RuleIDs{Fee: protocol.Digest("FEE_RULE_V2", f.Data()), Work: protocol.Digest("WORK_RULE_V2", w.Data()), Accounting: protocol.Digest("ACCOUNTING_V2", []byte("paid+discharged+remaining;credit=discharged+returned"))}
}
func (s Schedule) Validate() error {
	if s.MaxDepth == 0 || s.MaxAncestors == 0 || s.BaseExecution == 0 || s.BaseBytes == 0 {
		return protocol.ErrRule
	}
	_, e := NewEscrow(^uint64(0), s.Fee)
	return e
}
func PrepareVector(t protocol.TxBody, s Schedule) (protocol.AdmissionVector, error) {
	if t.Kind != protocol.FastTransfer || t.Fee.Source != protocol.OrgReserve {
		return nil, protocol.ErrUnsupported
	}
	if t.Rules != s.IDs() || t.Work.Depth > s.MaxDepth || t.Work.Ancestors > s.MaxAncestors {
		return nil, protocol.ErrRule
	}
	if _, e := NewEscrow(t.Fee.Maximum, s.Fee); e != nil {
		return nil, e
	}
	inputCost, e := protocol.MulDiv(uint64(len(t.Inputs)), s.InputExecution, 1)
	if e != nil {
		return nil, e
	}
	outputCost, e := protocol.MulDiv(uint64(len(t.Outputs)), s.OutputExecution, 1)
	if e != nil {
		return nil, e
	}
	exec, e := protocol.Add(s.BaseExecution, inputCost)
	if e != nil {
		return nil, e
	}
	exec, e = protocol.Add(exec, outputCost)
	if e != nil {
		return nil, e
	}
	body, e := t.MarshalBinary()
	if e != nil {
		return nil, e
	}
	// Reserve an envelope upper bound, independent of the chosen signature subset.
	retained, e := protocol.Add(s.BaseBytes, uint64(len(body)))
	if e != nil {
		return nil, e
	}
	retained, e = protocol.Add(retained, uint64(len(t.Inputs))*protocol.MaxCertificateBytes)
	if e != nil {
		return nil, e
	}
	if exec > uint64(t.Work.Execution) || retained > uint64(t.Work.Bytes) {
		return nil, ErrLimited
	}
	required := map[protocol.ResourceKind]uint64{protocol.ResourceFUEL: t.Fee.Maximum, protocol.ResourceExecution: exec, protocol.ResourceBytes: retained, protocol.ResourcePolicy: t.Fee.Maximum}
	if len(t.Admission) != len(required) {
		return nil, protocol.ErrRule
	}
	out := make(protocol.AdmissionVector, 0, len(required))
	seen := make(map[protocol.ResourceKind]bool)
	for _, r := range t.Admission {
		amount, ok := required[r.Key.Kind]
		if !ok || seen[r.Key.Kind] {
			return nil, protocol.ErrRule
		}
		seen[r.Key.Kind] = true
		account := t.Certifier
		if r.Key.Kind == protocol.ResourceFUEL {
			account = t.Fee.Account
		}
		if r.Key.Kind == protocol.ResourcePolicy {
			account = t.Fee.Policy
		}
		if r.Key.Account != account || r.Key.Version != t.Fee.Version {
			return nil, protocol.ErrRule
		}
		out = append(out, protocol.Allocation{Key: r.Key, Cap: amount})
	}
	return out, out.Validate()
}
func ValidateTransfer(t protocol.SignedTx, inputs []state.Creation) error {
	if e := t.VerifyAuth(); e != nil {
		return e
	}
	return validateTransferValues(t, inputs)
}
func validateTransferValues(t protocol.SignedTx, inputs []state.Creation) error {
	if len(inputs) != len(t.Body.Inputs) {
		return ErrMissing
	}
	owners := make(map[protocol.PublicKey]bool)
	for _, a := range t.Auth {
		owners[a.Owner] = true
	}
	var incoming, outgoing uint64
	for _, in := range inputs {
		if in.Output.Asset != protocol.AssetCAL || in.Output.Recipient.Route.Kind != protocol.OrgRoute || in.Output.Recipient.Route.Org != t.Body.Certifier || !owners[in.Output.Recipient.Owner] {
			return protocol.ErrAuth
		}
		var e error
		incoming, e = protocol.Add(incoming, in.Output.Amount)
		if e != nil {
			return e
		}
	}
	for _, out := range t.Body.Outputs {
		var e error
		outgoing, e = protocol.Add(outgoing, out.Amount)
		if e != nil {
			return e
		}
	}
	if incoming != outgoing {
		return ErrAccounting
	}
	return nil
}
func EqualCreation(a, b state.Creation) bool {
	// Both records originate from verified canonical objects, not arbitrary JSON.
	return a.Output == b.Output && (a.Fact == b.Fact || a.Final && a.Source == b.Fact || b.Final && b.Source == a.Fact)
}
func EvaluatePrepare(v state.ReadView, tx protocol.SignedTx, inputs []state.Creation, vector protocol.AdmissionVector, effects protocol.CertifiedEffects, worker uint32, parents []state.Outbox) ([]state.Change, protocol.SpendFactID, error) {
	fact := protocol.SpendID(tx.Body.ID(), vector, effects.Hash(), tx.Body.Rules)
	o := state.NewOverlay(v)
	if previous, found, e := state.Load[state.Approval](o, state.Key(state.KeyApproval, fact[:])); e != nil {
		return nil, fact, e
	} else if found {
		if previous.Fact != fact {
			return nil, fact, protocol.ErrRule
		}
		return nil, fact, nil
	}
	if observed, _, e := state.Load[bool](o, state.Key(state.KeyObserved, fact[:])); e != nil {
		return nil, fact, e
	} else if observed {
		return nil, fact, ErrConflict
	}
	id := tx.Body.ID()
	if prior, found, e := state.Load[protocol.TxID](o, state.Key(state.KeyIntent, tx.Body.Intent[:])); e != nil {
		return nil, fact, e
	} else if found && prior != id {
		return nil, fact, ErrConflict
	}
	for i, in := range tx.Body.Inputs {
		spend, _, e := state.Load[state.Spend](o, state.Key(state.KeySpend, in.Output[:]))
		if e != nil {
			return nil, fact, e
		}
		if spend.Consumed != (protocol.SpendFactID{}) || spend.Candidate != (protocol.SpendFactID{}) && spend.Candidate != fact {
			return nil, fact, ErrConflict
		}
		creation := inputs[i]
		old, found, e := state.Load[state.Creation](o, state.Key(state.KeyCreation, in.Output[:]))
		if e != nil {
			return nil, fact, e
		}
		if found {
			if !EqualCreation(old, creation) {
				return nil, fact, protocol.ErrRule
			}
		} else {
			if e = state.Put(o, state.Key(state.KeyCreation, in.Output[:]), creation); e != nil {
				return nil, fact, e
			}
		}
		spend.Candidate = fact
		if e = state.Put(o, state.Key(state.KeySpend, in.Output[:]), spend); e != nil {
			return nil, fact, e
		}
	}
	approval := state.Approval{Fact: fact, Tx: tx, Admission: vector, Effects: effects, Parents: nil}
	for i, x := range vector {
		ref := tx.Body.Admission[i]
		if !bytes.Equal(ref.Key.Encode(), x.Key.Encode()) {
			return nil, fact, protocol.ErrRule
		}
		grant, found, e := state.Load[state.Grant](o, state.Key(state.KeyGrant, x.Key.Encode()))
		if e != nil {
			return nil, fact, e
		}
		if !found || grant.ID != ref.Grant || grant.Key != x.Key || grant.Organization != tx.Body.Config {
			return nil, fact, protocol.ErrAuth
		}
		if x.Key.Kind == protocol.ResourcePolicy && grant.Subject != tx.Body.Subject {
			return nil, fact, protocol.ErrAuth
		}
		key := state.SliceKey(x.Key, worker)
		slice, found, e := state.Load[state.Slice](o, key)
		if e != nil {
			return nil, fact, e
		}
		if !found || slice.Available < x.Cap {
			return nil, fact, ErrLimited
		}
		slice.Available -= x.Cap
		slice.Reserved, e = protocol.Add(slice.Reserved, x.Cap)
		if e != nil {
			return nil, fact, e
		}
		if e = state.Put(o, key, slice); e != nil {
			return nil, fact, e
		}
		approval.Debits = append(approval.Debits, state.Debit{Key: x.Key, Cap: x.Cap, Worker: worker})
	}
	for _, material := range parents {
		approval.Parents = append(approval.Parents, material.Certificate)
	}
	if e := state.Put(o, state.Key(state.KeyApproval, fact[:]), approval); e != nil {
		return nil, fact, e
	}
	if e := state.Put(o, state.Key(state.KeyIntent, tx.Body.Intent[:]), id); e != nil {
		return nil, fact, e
	}
	// The durable approval is the signing intent; its exact vote can be rebuilt.
	if e := state.Put(o, state.Key(state.KeyOutbox, fact[:]), state.Outbox{Fact: fact, Origin: tx.Body.Certifier}); e != nil {
		return nil, fact, e
	}
	for _, material := range parents {
		if e := state.Put(o, state.Key(state.KeyOutbox, material.Fact[:]), material); e != nil {
			return nil, fact, e
		}
	}

	return o.Changes(), fact, nil
}
