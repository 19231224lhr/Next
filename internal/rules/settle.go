package rules

import (
	"bytes"
	"utxo/internal/state"
	"utxo/protocol"
)

type VerifiedCertificate struct {
	certificate protocol.TXCer
	raw         []byte
}

func VerifyCertificate(c protocol.TXCer, org protocol.OrgConfig, s Schedule) (VerifiedCertificate, error) {
	if e := c.Verify(org); e != nil {
		return VerifiedCertificate{}, e
	}
	vector, e := PrepareVector(c.Tx.Body, s)
	if e != nil {
		return VerifiedCertificate{}, e
	}
	if !bytes.Equal(vector.Encode(), c.Admission.Encode()) {
		return VerifiedCertificate{}, protocol.ErrRule
	}
	raw, e := c.MarshalBinary()
	if e != nil {
		return VerifiedCertificate{}, e
	}
	// Decode our own canonical copy so callers cannot mutate verified slices.
	frozen, e := protocol.DecodeCertificate(raw)
	return VerifiedCertificate{certificate: frozen, raw: raw}, e
}

type PublicPayment struct {
	Certificate []byte
	Fee         Escrow
	Settled     bool
}
type PublicUsage struct{ Reserved, Spent uint64 }

func AccountKey(owner protocol.Hash, asset protocol.Asset) []byte {
	return state.Key(state.KeyAccount, owner[:], []byte{byte(asset)})
}
func changeAmount(o *state.Overlay, key []byte, amount uint64, subtract bool) error {
	balance, _, e := state.Load[uint64](o, key)
	if e != nil {
		return e
	}
	if subtract {
		balance, e = protocol.Sub(balance, amount)
	} else {
		balance, e = protocol.Add(balance, amount)
	}
	if e != nil {
		return e
	}
	return state.Put(o, key, balance)
}
func updateUsage(o *state.Overlay, key protocol.ResourceKey, cap, spent uint64, grant uint64, register bool) error {
	k := state.Key(state.KeyUsage, key.Encode())
	usage, _, e := state.Load[PublicUsage](o, k)
	if e != nil {
		return e
	}
	if register {
		usage.Reserved, e = protocol.Add(usage.Reserved, cap)
	} else {
		usage.Reserved, e = protocol.Sub(usage.Reserved, cap)
		if e == nil {
			usage.Spent, e = protocol.Add(usage.Spent, spent)
		}
	}
	if e != nil {
		return e
	}
	total, e := protocol.Add(usage.Reserved, usage.Spent)
	if e != nil {
		return e
	}
	if total > grant {
		return ErrLimited
	}
	return state.Put(o, k, usage)
}
func payStage(o *state.Overlay, p *PublicPayment, action FeeAction, c protocol.TXCer) error {
	old := p.Fee
	next, e := old.Complete(action)
	if e != nil {
		return e
	}
	beneficiary := c.Tx.Body.Certifier
	if action == FeeSettle {
		beneficiary = protocol.Digest("COMMITTEE_REWARDS", c.Tx.Body.Network[:])
	}
	if e = changeAmount(o, state.Key(state.KeyReward, beneficiary[:]), next.Rewards-old.Rewards, false); e != nil {
		return e
	}
	if e = changeAmount(o, state.Key(state.KeyBurned), next.Burned-old.Burned, false); e != nil {
		return e
	}
	if next.Refunded > old.Refunded {
		if e = changeAmount(o, AccountKey(c.Tx.Body.Fee.Account, protocol.AssetFUEL), next.Refunded-old.Refunded, false); e != nil {
			return e
		}
	}
	p.Fee = next
	return nil
}

// EvaluateSettlement preserves REGISTER when principal dependencies are missing.
// Each command is retried under the same certificate identity; completed actions
// do not charge fees again. Only a public finalized creation satisfies a dependency.
func EvaluateSettlement(v state.ReadView, verified VerifiedCertificate, s Schedule) (state.Transition, error) {
	c := verified.certificate
	t := c.Tx.Body
	if len(verified.raw) == 0 {
		return state.Transition{}, protocol.ErrAuth
	}
	o := state.NewOverlay(v)
	key := state.Key(state.KeyPayment, c.QC.Fact[:])
	p, registered, e := state.Load[PublicPayment](o, key)
	if e != nil {
		return state.Transition{}, e
	}
	if registered && p.Settled && p.Fee.Closed {
		return state.Transition{}, nil
	}
	grants := make([]state.Grant, len(c.Admission))
	for i, allocation := range c.Admission {
		g, ok, e := state.Load[state.Grant](o, state.Key(state.KeyGrant, allocation.Key.Encode()))
		if e != nil {
			return state.Transition{}, e
		}
		if !ok || g.ID != t.Admission[i].Grant || g.Organization != t.Config || g.Key != allocation.Key || (g.Key.Kind == protocol.ResourcePolicy && g.Subject != t.Subject) {
			return state.Transition{}, protocol.ErrAuth
		}
		grants[i] = g
	}
	if !registered {
		// Intent binds retries even if clients change certificate signature subsets.
		prior, found, e := state.Load[protocol.TxID](o, state.Key(state.KeyIntent, t.Intent[:]))
		if e != nil {
			return state.Transition{}, e
		}
		if found && prior != t.ID() {
			return state.Transition{}, ErrConflict
		}
		for i, a := range c.Admission {
			if e = updateUsage(o, a.Key, a.Cap, 0, grants[i].Amount, true); e != nil {
				return state.Transition{}, e
			}
		}
		if e = changeAmount(o, AccountKey(t.Fee.Account, protocol.AssetFUEL), t.Fee.Maximum, true); e != nil {
			return state.Transition{}, e
		}
		escrow, e := NewEscrow(t.Fee.Maximum, s.Fee)
		if e != nil {
			return state.Transition{}, e
		}
		p = PublicPayment{Certificate: verified.raw, Fee: escrow}
		if e = payStage(o, &p, FeeRegister, c); e != nil {
			return state.Transition{}, e
		}
		if e = state.Put(o, state.Key(state.KeyIntent, t.Intent[:]), t.ID()); e != nil {
			return state.Transition{}, e
		}
	}
	deferred := false
	inputs := make([]state.Creation, len(t.Inputs))
	for i, in := range t.Inputs {
		creation, found, e := state.Load[state.Creation](o, state.Key(state.KeyCreation, in.Output[:]))
		if e != nil {
			return state.Transition{}, e
		}
		if !found || !creation.Final {
			deferred = true
			continue
		}
		if in.Kind == protocol.CertificateInput {
			// Final creation carries the original spend identity independently of its proof.
			if creation.Source != in.Evidence {
				return state.Transition{}, protocol.ErrAuth
			}
		} else if creation.Fact != in.Evidence {
			return state.Transition{}, protocol.ErrAuth
		}
		spend, _, e := state.Load[state.Spend](o, state.Key(state.KeySpend, in.Output[:]))
		if e != nil {
			return state.Transition{}, e
		}
		if spend.Consumed != (protocol.SpendFactID{}) && spend.Consumed != c.QC.Fact {
			return state.Transition{}, ErrConflict
		}
		inputs[i] = creation
	}
	if deferred {
		if e = state.Put(o, key, p); e != nil {
			return state.Transition{}, e
		}
		return state.Transition{Changes: o.Changes()}, nil
	}
	if e = validateTransferValues(c.Tx, inputs); e != nil {
		return state.Transition{}, e
	}
	var facts []protocol.FinalFact
	fact := func(kind protocol.FactKind, key protocol.Hash, payload []byte) protocol.FinalFact {
		return protocol.FinalFact{Kind: kind, Key: key, Revision: 1, Network: t.Network, Rules: t.Rules, Payload: payload}
	}
	for _, in := range t.Inputs {
		if e = state.Put(o, state.Key(state.KeySpend, in.Output[:]), state.Spend{Consumed: c.QC.Fact}); e != nil {
			return state.Transition{}, e
		}
		payload := new(protocol.Encoder)
		payload.Fixed(in.Output[:])
		payload.Fixed(c.QC.Fact[:])
		txid := t.ID()
		payload.Fixed(txid[:])
		facts = append(facts, fact(protocol.FactOutputConsumed, protocol.Hash(in.Output), payload.Data()))
	}
	for i, id := range c.Effects.Outputs {
		output := protocol.SettledOutput{Spend: c.QC.Fact, ID: id, Transaction: t.ID(), Index: uint32(i), Output: t.Outputs[i]}
		payload, e := output.MarshalBinary()
		if e != nil {
			return state.Transition{}, e
		}
		f := fact(protocol.FactOutputCreated, protocol.Hash(id), payload)
		if e = state.Put(o, state.Key(state.KeyCreation, id[:]), state.Creation{Output: t.Outputs[i], Fact: f.ID(), Source: protocol.Hash(c.QC.Fact), Final: true}); e != nil {
			return state.Transition{}, e
		}
		facts = append(facts, f)
	}
	if e = payStage(o, &p, FeeSettle, c); e != nil {
		return state.Transition{}, e
	}
	p.Settled = true
	if e = payStage(o, &p, FeeClose, c); e != nil {
		return state.Transition{}, e
	}
	paid, e := protocol.Add(p.Fee.Rewards, p.Fee.Burned)
	if e != nil {
		return state.Transition{}, e
	}
	for i, a := range c.Admission {
		receipt := protocol.CreditReceipt{Spend: c.QC.Fact, Resource: a.Key, Original: a.Cap, Revision: 1}
		kind := protocol.FactCredit
		switch a.Key.Kind {
		case protocol.ResourceFUEL, protocol.ResourcePolicy:
			receipt.Paid = paid
			receipt.Discharged = a.Cap - paid
		case protocol.ResourceExecution:
			receipt.Discharged = a.Cap
			kind = protocol.FactWork
		case protocol.ResourceBytes:
			receipt.Discharged = a.Cap
			kind = protocol.FactCustody
		default:
			return state.Transition{}, protocol.ErrUnsupported
		}
		if e = updateUsage(o, a.Key, a.Cap, receipt.Paid, grants[i].Amount, false); e != nil {
			return state.Transition{}, e
		}
		payload, e := receipt.MarshalBinary()
		if kind == protocol.FactCustody {
			payload, e = (protocol.CustodyReceipt{Credit: receipt, Certificate: protocol.Digest("CERTIFICATE_OBJECT", verified.raw), Effects: c.Effects.Hash()}).MarshalBinary()
		}
		if e != nil {
			return state.Transition{}, e
		}
		facts = append(facts, fact(kind, receipt.Key(), payload))
	}
	closePayload := new(protocol.Encoder)
	closePayload.Fixed(c.Effects.Fee[:])
	closePayload.U64(p.Fee.Maximum)
	closePayload.U64(p.Fee.Rewards)
	closePayload.U64(p.Fee.Burned)
	closePayload.U64(p.Fee.Refunded)
	facts = append(facts, fact(protocol.FactFeeClosed, protocol.Hash(c.Effects.Fee), closePayload.Data()))
	if e = state.Put(o, key, p); e != nil {
		return state.Transition{}, e
	}
	return state.Transition{Changes: o.Changes(), Facts: facts}, nil
}
