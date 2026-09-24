package wallet

import (
	"crypto/ed25519"
	"testing"
	"utxo/finality"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestMultiOwnerFeeBlockAndBinding(t *testing.T) {
	network := protocol.Digest("test-network")
	owners := map[protocol.PublicKey]bool{}
	var entries []finality.ExecutedTx
	var fees []protocol.FeeOutput
	for i := byte(1); i <= 3; i++ {
		seed := make([]byte, 32)
		seed[0] = i
		d := protocol.NewDescriptor(network, protocol.Route{Kind: protocol.CommitteeRoute}, ed25519.NewKeyFromSeed(seed))
		if i < 3 {
			owners[d.Owner] = true
		}
		f := protocol.FeeOutput{Transaction: protocol.TxID(protocol.Digest("tx", []byte{i})), Index: protocol.FeeRefundIndex, Output: protocol.Output{Asset: protocol.AssetFUEL, Amount: 906, Recipient: d}}
		fees = append(fees, f)
		raw, e := (protocol.ExecutionResult{Applied: true, FeeOutputs: []protocol.FeeOutput{f}}).MarshalBinary()
		if e != nil {
			t.Fatal(e)
		}
		// A repair command has no ordinary payment outputs, but may release fees.
		entries = append(entries, finality.ExecutedTx{Bytes: []byte("REPAIR3\x00"), Data: raw})
	}
	// Test parsed fee effects separately from block proof construction.
	coins, e := collectBlockCoins(network, owners, entries)
	if e != nil {
		t.Fatal(e)
	}
	if len(coins) != 2 {
		t.Fatalf("got %d fee coins", len(coins))
	}
	db := store.NewMemory()
	defer db.Close()
	if e = db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if e := applyBlockCoins(o, coins); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	}); e != nil {
		t.Fatal(e)
	}
	for _, f := range fees[:2] {
		if e = db.View(func(v state.ReadView) error {
			c, ok, e := state.Load[DirectCoin](v, DirectCoinKey(protocol.OutputIdentity(network, f.Transaction, f.Index), 0))
			if !ok || c.Output != f.Output || c.Final == (protocol.Hash{}) {
				t.Fatal("missing owner refund")
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
	}
	bad := coins
	bad[0].coin.Output.Amount++
	e = db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		e := applyBlockCoins(o, bad)
		return o.Changes(), e
	})
	if e == nil {
		t.Fatal("changed output accepted")
	}
}
