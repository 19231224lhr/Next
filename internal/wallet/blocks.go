package wallet

import (
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/state"
	"utxo/protocol"
)

func (w *Wallet) PrepareBlock(b finality.VerifiedBlock) (blockfollow.Apply, error) {
	return PrepareOwnersBlock(w.network, map[protocol.PublicKey]bool{w.owner: true})(b)
}

// PrepareOwnersBlock shares verified block parsing across a fixed wallet group.
// The caller commits all owned effects and advances the group's cursor once.
// owners must remain immutable for the lifetime of the follower.
func PrepareOwnersBlock(network protocol.Hash, owners map[protocol.PublicKey]bool) blockfollow.Prepare {
	return func(b finality.VerifiedBlock) (blockfollow.Apply, error) {
		coins, err := collectBlockCoins(network, owners, b.Transactions())
		if err != nil {
			return nil, err
		}
		return func(o *state.Overlay) error { return applyBlockCoins(o, coins) }, nil
	}
}

type blockCoin struct {
	key  []byte
	coin DirectCoin
}

func collectBlockCoins(network protocol.Hash, owners map[protocol.PublicKey]bool, entries []finality.ExecutedTx) ([]blockCoin, error) {
	var coins []blockCoin
	for _, entry := range entries {
		if entry.Code != 0 || len(entry.Data) == 0 {
			continue
		}
		result, err := protocol.DecodeExecution(entry.Data)
		if err != nil {
			return nil, err
		}
		if !result.Applied {
			continue
		}
		for _, fee := range result.FeeOutputs {
			if fee.Output.Recipient.Verify(network) != nil {
				return nil, protocol.ErrAuth
			}
			if !owners[fee.Output.Recipient.Owner] {
				continue
			}
			id := protocol.OutputIdentity(network, fee.Transaction, fee.Index)
			coin := DirectCoin{Output: fee.Output, Index: fee.Index, Final: protocol.CreationIdentity(network, fee.Transaction, fee.Index, 0)}
			coins = append(coins, blockCoin{DirectCoinKey(id, 0), coin})
		}
		if protocol.IsRepairInput(entry.Bytes) {
			continue
		}
		pay, err := protocol.DecodeDirectSubmission(entry.Bytes)
		if err != nil {
			return nil, err
		}
		if pay.Tx.Body.Network != network {
			return nil, protocol.ErrAuth
		}
		late := map[uint32]bool{}
		for _, i := range result.LateOutputs {
			if int(i) >= len(pay.Tx.Body.Outputs) {
				return nil, protocol.ErrRule
			}
			late[i] = true
		}
		for i, out := range pay.Tx.Body.Outputs {
			if !owners[out.Recipient.Owner] {
				continue
			}
			instance := uint8(0)
			if late[uint32(i)] {
				instance = 1
			}
			id := pay.Summary().OutputID(uint32(i))
			coin := DirectCoin{Output: out, Instance: instance, Index: uint32(i), Final: protocol.CreationIdentity(network, pay.Tx.ID(), uint32(i), instance)}
			coins = append(coins, blockCoin{DirectCoinKey(id, instance), coin})
		}
	}
	return coins, nil
}

func applyBlockCoins(o *state.Overlay, coins []blockCoin) error {
	for _, c := range coins {
		old, found, err := state.Load[DirectCoin](o, c.key)
		if err != nil {
			return err
		}
		if found && old.Output != c.coin.Output {
			return protocol.ErrAuth
		}
		if err = state.Put(o, c.key, c.coin); err != nil {
			return err
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
