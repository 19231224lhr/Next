//go:build comet_v3

package redaction_test

import (
	cmtstore "github.com/cometbft/cometbft/store"
	"testing"
	"utxo/crypto/chameleon"
	"utxo/internal/redaction"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

// Construct each revision with genuine threshold contributions, without
// installing intermediate bodies into the physical block store.
func derivePartitionedRepairs(t *testing.T, v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, signers []chameleon.Signer, outputs []protocol.OutputID, size int) ([]byte, []chameleon.Opening) {
	t.Helper()
	o := state.NewOverlay(v)
	var finalParts []chameleon.Opening
	for start := 0; start < len(outputs); start += size {
		var singles []protocol.RepairInput
		for _, output := range outputs[start:min(start+size, len(outputs))] {
			c, pay, err := redaction.InputTarget(o, bs, p, output, 1700000032)
			if err != nil {
				t.Fatal(err)
			}
			var shares []chameleon.Contribution
			for _, s := range signers[:3] {
				share, err := redaction.InputShare(o, bs, p, s, output, 1700000032)
				if err != nil {
					t.Fatal(err)
				}
				shares = append(shares, share)
			}
			c, err = redaction.ReplaceInput(p, c, pay, shares)
			if err != nil {
				t.Fatal(err)
			}
			singles = append(singles, c)
		}
		batch, err := redaction.BuildBatch(o, bs, p, singles, 1700000032)
		if err != nil {
			t.Fatal(err)
		}
		var votes [][]chameleon.Contribution
		for _, s := range signers[:3] {
			share, err := redaction.BatchPartShares(o, bs, p, s, batch, 1700000032)
			if err != nil {
				t.Fatal(err)
			}
			votes = append(votes, share)
		}
		batch, err = redaction.CompleteBatchParts(o, bs, p, batch, 1700000032, votes)
		if err != nil {
			t.Fatal(err)
		}
		tr, err := redaction.ExecuteBatch(o, bs, p, batch, 50, 1700000032)
		if err != nil {
			t.Fatal(err)
		}
		o.Apply(tr.Changes)
		finalParts = batch.Parts
	}
	_, final, err := redaction.Canonical(o, bs, 1)
	if err != nil {
		t.Fatal(err)
	}
	return final.Body, finalParts
}
