//go:build comet_v3

package redaction

import (
	"bytes"

	cmtstore "github.com/cometbft/cometbft/store"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

// Observe checks the actual BlockStore against the committed repair. Comparing
// only the repaired funding slot also permits later repairs to other inputs.
func Observe(v state.ReadView, bs *cmtstore.BlockStore, id protocol.Hash) (Observation, error) {
	var s Observation
	task, found, err := state.Load[Task](v, TaskKey(id))
	if err != nil || !found {
		return s, err
	}
	c := task.Command
	s.Committed, s.CommitHeight, s.TargetHeight = true, task.Height, c.Height
	original, err := bs.LoadOriginalBlock(c.Height)
	if err != nil {
		return s, err
	}
	current := bs.LoadBlock(c.Height)
	meta := bs.LoadBlockMeta(c.Height)
	part := bs.LoadBlockPart(c.Height, 0)
	if original == nil || current == nil || meta == nil || part == nil || int(c.Transaction) >= len(current.Data.Txs) || int(c.Transaction) >= len(original.Data.Txs) {
		return s, rules.ErrMissing
	}
	if part.Redaction != nil {
		s.Revision = part.Redaction.Revision
	}
	before, err := protocol.DecodeDirectSubmission(original.Data.Txs[c.Transaction])
	if err != nil {
		return s, err
	}
	after, err := protocol.DecodeDirectSubmission(current.Data.Txs[c.Transaction])
	if err != nil {
		return s, err
	}
	expected, err := protocol.DecodeDirectSubmission(c.TransactionBytes)
	if err != nil {
		return s, err
	}
	if int(c.Input) >= len(before.Tx.Funding) || int(c.Input) >= len(after.Tx.Funding) || int(c.Input) >= len(expected.Tx.Funding) {
		return s, protocol.ErrRule
	}
	s.IdentityStable = bytes.Equal(original.Hash(), current.Hash()) && bytes.Equal(current.Hash(), meta.BlockID.Hash) && bytes.Equal(original.DataHash, current.DataHash) && before.Tx.ID() == after.Tx.ID()
	s.BytesChanged = !bytes.Equal(original.Data.Txs[c.Transaction], current.Data.Txs[c.Transaction])
	s.Materialized = s.Revision >= c.Base+1 && s.IdentityStable && s.BytesChanged && before.Tx.Funding[c.Input] != after.Tx.Funding[c.Input] && after.Tx.Funding[c.Input] == expected.Tx.Funding[c.Input]
	return s, nil
}
