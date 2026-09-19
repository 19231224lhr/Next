package wallet

import (
	"utxo/finality"
	"utxo/internal/state"
	"utxo/protocol"
)

func (w *Wallet) ApplyBlock(o *state.Overlay, b finality.VerifiedBlock) error {
	for _, entry := range b.Transactions() {
		if entry.Code != 0 || len(entry.Data) == 0 {
			continue
		}
		result, err := protocol.DecodeExecution(entry.Data)
		if err != nil {
			return err
		}
		if !result.Applied || protocol.IsRepairInput(entry.Bytes) {
			continue
		}
		pay, err := protocol.DecodeDirectPayment(entry.Bytes)
		if err != nil {
			return err
		}
		if pay.Tx.Body.Network != w.network {
			return protocol.ErrAuth
		}
		late := map[uint32]bool{}
		for _, i := range result.LateOutputs {
			if int(i) >= len(pay.Tx.Body.Outputs) {
				return protocol.ErrRule
			}
			late[i] = true
		}
		for i, out := range pay.Tx.Body.Outputs {
			if out.Recipient.Owner != w.owner {
				continue
			}
			instance := uint8(0)
			if late[uint32(i)] {
				instance = 1
			}
			id := pay.Certificate.Summary.OutputID(uint32(i))
			key := DirectCoinKey(id, instance)
			old, found, err := state.Load[DirectCoin](o, key)
			if err != nil {
				return err
			}
			if found && old.Output != out {
				return protocol.ErrAuth
			}
			coin := DirectCoin{Output: out, Instance: instance, Index: uint32(i), Final: protocol.CreationIdentity(w.network, pay.Tx.ID(), uint32(i), instance)}
			if err = state.Put(o, key, coin); err != nil {
				return err
			}
		}
	}
	return nil
}
func (w *Wallet) DirectFinal(id protocol.OutputID, instance uint8) (bool, error) {
	var final bool
	err := w.db.View(func(v state.ReadView) error {
		coin, found, err := state.Load[DirectCoin](v, DirectCoinKey(id, instance))
		final = found && coin.Final != (protocol.Hash{})
		return err
	})
	return final, err
}
