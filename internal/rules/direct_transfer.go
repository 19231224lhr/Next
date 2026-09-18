package rules

import (
	"utxo/internal/state"
	"utxo/protocol"
)

type VerifiedDirect struct{ tx protocol.SignedTx }

func VerifyDirect(tx protocol.SignedTx, network protocol.Hash, s Schedule) (VerifiedDirect, error) {
	if e := tx.VerifyAuth(); e != nil {
		return VerifiedDirect{}, e
	}
	if tx.Body.Kind != protocol.DirectTransfer || tx.Body.Network != network || tx.Body.Rules != s.IDs() {
		return VerifiedDirect{}, protocol.ErrRule
	}
	cost, e := protocol.Add(s.Fee.Settle, s.Fee.Burn)
	if e != nil {
		return VerifiedDirect{}, e
	}
	if cost > tx.Body.Fee.Maximum {
		return VerifiedDirect{}, ErrLimited
	}
	raw, e := tx.MarshalBinary()
	if e != nil {
		return VerifiedDirect{}, e
	}
	copy, e := protocol.DecodeSignedTx(raw)
	return VerifiedDirect{tx: copy}, e
}
func EvaluateDirectTransfer(v state.ReadView, verified VerifiedDirect, s Schedule) (state.Transition, error) {
	tx := verified.tx
	t := tx.Body
	if t.Kind != protocol.DirectTransfer {
		return state.Transition{}, protocol.ErrAuth
	}
	id := t.ID()
	factID := protocol.SpendFactID(protocol.Digest("DIRECT_SPEND", t.Network[:], id[:]))
	o := state.NewOverlay(v)
	key := state.Key(state.KeyDirect, id[:])
	if done, _, e := state.Load[bool](o, key); e != nil {
		return state.Transition{}, e
	} else if done {
		return state.Transition{}, nil
	}
	if prior, found, e := state.Load[protocol.TxID](o, state.Key(state.KeyIntent, t.Intent[:])); e != nil {
		return state.Transition{}, e
	} else if found && prior != id {
		return state.Transition{}, ErrConflict
	}
	owners := make(map[protocol.PublicKey]bool)
	for _, a := range tx.Auth {
		owners[a.Owner] = true
	}
	totals := [2]uint64{}
	for group, inputs := range [][]protocol.Input{t.Inputs, t.Fee.Inputs} {
		for _, in := range inputs {
			creation, found, e := state.Load[state.Creation](o, state.Key(state.KeyCreation, in.Output[:]))
			if e != nil {
				return state.Transition{}, e
			}
			if !found || !creation.Final || creation.Fact != in.Evidence {
				return state.Transition{}, ErrMissing
			}
			asset := protocol.AssetCAL
			if group == 1 {
				asset = protocol.AssetFUEL
			}
			if creation.Output.Asset != asset || creation.Output.Recipient.Route.Kind != protocol.CommitteeRoute || !owners[creation.Output.Recipient.Owner] {
				return state.Transition{}, protocol.ErrAuth
			}
			spend, _, e := state.Load[state.Spend](o, state.Key(state.KeySpend, in.Output[:]))
			if e != nil {
				return state.Transition{}, e
			}
			if spend.Consumed != (protocol.SpendFactID{}) {
				return state.Transition{}, ErrConflict
			}
			totals[group], e = protocol.Add(totals[group], creation.Output.Amount)
			if e != nil {
				return state.Transition{}, e
			}
		}
	}
	var outgoing uint64
	for _, out := range t.Outputs {
		var e error
		outgoing, e = protocol.Add(outgoing, out.Amount)
		if e != nil {
			return state.Transition{}, e
		}
	}
	if outgoing != totals[0] || totals[1] < t.Fee.Maximum {
		return state.Transition{}, ErrAccounting
	}
	cost, e := protocol.Add(s.Fee.Settle, s.Fee.Burn)
	if e != nil {
		return state.Transition{}, e
	}
	change, e := protocol.Sub(totals[1], cost)
	if e != nil {
		return state.Transition{}, e
	}
	makeFact := func(kind protocol.FactKind, key protocol.Hash, body []byte) protocol.FinalFact {
		return protocol.FinalFact{Kind: kind, Key: key, Revision: 1, Network: t.Network, Rules: t.Rules, Payload: body}
	}
	var facts []protocol.FinalFact
	for _, group := range [][]protocol.Input{t.Inputs, t.Fee.Inputs} {
		for _, in := range group {
			if e = state.Put(o, state.Key(state.KeySpend, in.Output[:]), state.Spend{Consumed: factID}); e != nil {
				return state.Transition{}, e
			}
			body := new(protocol.Encoder)
			body.Fixed(in.Output[:])
			body.Fixed(factID[:])
			body.Fixed(id[:])
			facts = append(facts, makeFact(protocol.FactOutputConsumed, protocol.Hash(in.Output), body.Data()))
		}
	}
	outputs := append([]protocol.Output(nil), t.Outputs...)
	if change > 0 {
		outputs = append(outputs, protocol.Output{Asset: protocol.AssetFUEL, Amount: change, Recipient: t.Fee.Refund})
	}
	for i, out := range outputs {
		outputID := protocol.OutputIdentity(t.Network, id, uint32(i))
		payload, e := (protocol.SettledOutput{Spend: factID, ID: outputID, Transaction: id, Index: uint32(i), Output: out}).MarshalBinary()
		if e != nil {
			return state.Transition{}, e
		}
		f := makeFact(protocol.FactOutputCreated, protocol.Hash(outputID), payload)
		facts = append(facts, f)
		if e = state.Put(o, state.Key(state.KeyCreation, outputID[:]), state.Creation{Output: out, Fact: f.ID(), Source: protocol.Hash(factID), Final: true}); e != nil {
			return state.Transition{}, e
		}
	}
	beneficiary := protocol.Digest("COMMITTEE_REWARDS", t.Network[:])
	if e = changeAmount(o, state.Key(state.KeyReward, beneficiary[:]), s.Fee.Settle, false); e != nil {
		return state.Transition{}, e
	}
	if e = changeAmount(o, state.Key(state.KeyBurned), s.Fee.Burn, false); e != nil {
		return state.Transition{}, e
	}
	feeID := protocol.FeeIdentity(t)
	body := new(protocol.Encoder)
	body.Fixed(feeID[:])
	body.U64(t.Fee.Maximum)
	body.U64(s.Fee.Settle)
	body.U64(s.Fee.Burn)
	body.U64(t.Fee.Maximum - cost)
	facts = append(facts, makeFact(protocol.FactFeeClosed, protocol.Hash(feeID), body.Data()))
	if e = state.Put(o, key, true); e != nil {
		return state.Transition{}, e
	}
	if e = state.Put(o, state.Key(state.KeyIntent, t.Intent[:]), id); e != nil {
		return state.Transition{}, e
	}
	return state.Transition{Changes: o.Changes(), Facts: facts}, nil
}
