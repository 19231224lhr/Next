package rules

import (
	"utxo/internal/state"
	"utxo/protocol"
)

// ValidateDirectFee reads authenticated final outputs, not caller-supplied
// amounts. Signature verification is performed by PrepareDirectVector first.
// Spend conflict policy is applied by the caller in the same transaction.
func ValidateDirectFee(v state.ReadView, tx protocol.FastTx) (uint64, error) {
	if tx.Body.Fee.Source != protocol.OwnerFinalUTXO {
		return 0, nil
	}
	var total uint64
	for _, in := range tx.Body.Fee.Inputs {
		c, ok, err := state.Load[state.Creation](v, DirectCreationKey(in.Output, 0))
		if err != nil {
			return 0, err
		}
		if !ok || !c.Final || in.Kind != protocol.FinalInput || c.Fact != in.Evidence {
			return 0, ErrMissing
		}
		out := c.Output
		if out.Asset != protocol.AssetFUEL || out.Amount == 0 || out.Recipient.Route.Kind != protocol.OrgRoute || out.Recipient.Route.Org != tx.Body.Certifier {
			return 0, protocol.ErrAuth
		}
		signed := false
		for _, a := range tx.Auth {
			if a.Owner == out.Recipient.Owner {
				signed = true
				break
			}
		}
		if !signed {
			return 0, protocol.ErrAuth
		}
		total, err = protocol.Add(total, out.Amount)
		if err != nil {
			return 0, err
		}
	}
	if total < tx.Body.Fee.Maximum {
		return 0, ErrLimited
	}
	return total, nil
}

func (e *directEval) feeOutput(p *directPayment, index uint32, amount uint64) error {
	if amount == 0 {
		return nil
	}
	f := protocol.FeeOutput{Transaction: p.Summary.Tx, Index: index, Output: protocol.Output{Asset: protocol.AssetFUEL, Amount: amount, Recipient: p.FeeRefund}}
	id := protocol.OutputIdentity(e.network, f.Transaction, index)
	c := state.Creation{Output: f.Output, Final: true, Fact: protocol.CreationIdentity(e.network, f.Transaction, index, 0)}
	if err := state.Put(e.o, DirectCreationKey(id, 0), c); err != nil {
		return err
	}
	e.resultData.FeeOutputs = append(e.resultData.FeeOutputs, f)
	return nil
}
