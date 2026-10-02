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
		result, _, err := protocol.DecodePublicExecution(network, entry.Bytes, entry.Data)
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
		if protocol.IsRepairInput(entry.Bytes) || protocol.IsRepairBatch(entry.Bytes) || protocol.IsCompensationDecision(entry.Bytes) {
			continue
		}
		pay, err := protocol.DecodeDirectSubmission(entry.Bytes)
		if err != nil {
			return nil, err
		}
		if pay.Tx.Body.Network != network {
			return nil, protocol.ErrAuth
		}
		recovered := map[uint32]bool{}
		for _, i := range result.RecoveredOutputs {
			if int(i) >= len(pay.Tx.Body.Outputs) {
				return nil, protocol.ErrRule
			}
			recovered[i] = true
		}
		for i, out := range pay.Tx.Body.Outputs {
			if !owners[out.Recipient.Owner] {
				continue
			}
			id := pay.Summary().OutputID(uint32(i))
			coin := DirectCoin{Output: out, Index: uint32(i), Recovered: recovered[uint32(i)]}
			if !coin.Recovered {
				coin.Final = protocol.CreationIdentity(network, pay.Tx.ID(), uint32(i), 0)
			}
			coins = append(coins, blockCoin{DirectCoinKey(id, 0), coin})
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
		final = found && !coin.Recovered && coin.Final != (protocol.Hash{})
		return err
	})
	return final, err
}

// DirectRecovered observes public source execution and reserve repayment. It
// deliberately does not report an owned final output or a new wallet balance.
func (w *Wallet) DirectRecovered(id protocol.OutputID) (bool, error) {
	var recovered bool
	err := w.db.View(func(v state.ReadView) error {
		coin, found, err := state.Load[DirectCoin](v, DirectCoinKey(id, 0))
		recovered = found && coin.Recovered
		return err
	})
	return recovered, err
}
