//go:build comet_v3

package redaction_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/cometbft/cometbft/types"
	"utxo/internal/redaction"
	"utxo/internal/state"
	"utxo/protocol"
)

// This runnable example exports a byte-oriented archival view from an already
// verified local replica. It is not a remote proof or a deployed business client.
func TestHistoricalExportConsumer(t *testing.T) {
	f := newReaderFixture(t, t.TempDir(), 4, 1)
	type exported struct {
		Prefix, Height               int64
		Transaction, Input           uint32
		BlockID                      types.BlockID
		Revision                     uint64
		Task                         protocol.Hash
		OriginalBody, AuthorizedBody []byte
		HistoricalFunding            protocol.Funding
		Economic                     readerAnswer
	}
	read := func() (out exported) {
		readerOK(t, f.db.View(func(v state.ReadView) error {
			b, revision, err := redaction.Canonical(v, f.revised, 1)
			if err != nil {
				return err
			}
			task, found, err := redaction.LoadTask(v, f.batch.ID())
			if err != nil {
				return err
			}
			if !found || !bytes.Equal(task.Body, revision.Body) || task.Command.Base+1 != revision.Number {
				return protocol.ErrAuth
			}
			parts, err := types.NewRedactablePartSet(revision.Body, types.BlockPartSizeBytes, 1, revision.Number, task.Command.Parts)
			if err != nil {
				return err
			}
			id := f.original.LoadBlockMeta(1).BlockID
			if !id.Equals(types.BlockID{Hash: b.Hash(), PartSetHeader: parts.Header()}) {
				return protocol.ErrAuth
			}
			pay, err := protocol.DecodeDirectSubmission(b.Data.Txs[0])
			if err != nil {
				return err
			}
			if err = readerCrypto(pay, f.policy); err != nil {
				return err
			}
			if err = readerInputCH(pay, f.policy); err != nil {
				return err
			}
			answers, err := readerResolve(v, f.policy, pay, 1, 0, f.height, true, nil)
			if err != nil {
				return err
			}
			original, err := f.revised.LoadOriginalBlock(1)
			if err != nil {
				return err
			}
			pb, err := original.ToProto()
			if err != nil {
				return err
			}
			raw, err := pb.Marshal()
			if err != nil {
				return err
			}
			before, err := protocol.DecodeDirectSubmission(original.Data.Txs[0])
			if err != nil {
				return err
			}
			if before.Tx.ID() != pay.Tx.ID() || before.Tx.Body.Outputs[0] != pay.Tx.Body.Outputs[0] || before.Tx.Funding[0].Kind != protocol.OriginalFunding || pay.Tx.Funding[0].Kind != protocol.ReserveFunding {
				return protocol.ErrAuth
			}
			out = exported{Prefix: f.height, Height: 1, BlockID: id, Revision: revision.Number, Task: f.batch.ID(), OriginalBody: raw, AuthorizedBody: revision.Body, HistoricalFunding: pay.Tx.Funding[0], Economic: answers[0]}
			return nil
		}))
		return
	}
	before := read()
	readerRecoveryCheck(t, f)
	after := read()
	if !bytes.Equal(before.AuthorizedBody, after.AuthorizedBody) || before.Economic.Status == after.Economic.Status {
		t.Fatal("historical provenance and current repayment state conflated")
	}
	if path := os.Getenv("UTXO_HISTORY_EXPORT"); path != "" {
		raw, err := json.MarshalIndent([]exported{before, after}, "", "  ")
		readerOK(t, err)
		readerOK(t, os.WriteFile(path, raw, 0644))
	}
	t.Log("original-coordinate export verified: authorization, full BlockID, owner signatures, direct certificates, immutable outputs, and paid provenance after repayment")
}
