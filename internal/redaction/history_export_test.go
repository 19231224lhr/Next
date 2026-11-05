//go:build comet_v3

package redaction_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/cometbft/cometbft/types"
	"utxo/internal/redaction"
	"utxo/internal/rules"
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
		IndexedEconomic              readerAnswer
		MaterializedEconomic         readerAnswer
		OriginalCommitVerified       bool
		RevisedCommitVerified        bool
		UnadaptedPartsRejected       bool
	}
	var materializedView []byte
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
			// All three alternatives use the same executed read view. A plain
			// materialized row retains identity pointers without claiming to be
			// a replacement block body or an independently authenticated proof.
			indexed, err := readerResolve(v, f.policy, before, 1, 0, f.height, false, nil)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(indexed, answers) {
				return protocol.ErrAuth
			}
			type fundingView struct {
				BlockID types.BlockID
				Rows    []readerAnswer
			}
			if materializedView == nil {
				rows := append([]readerAnswer(nil), indexed...)
				for i := range rows {
					rows[i].Prefix, rows[i].Status = 0, 0
				}
				materializedView, err = json.Marshal(fundingView{id, rows})
				if err != nil {
					return err
				}
			}
			var view fundingView
			if err = json.Unmarshal(materializedView, &view); err != nil {
				return err
			}
			for i := range view.Rows {
				ob, found, err := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(view.Rows[i].Output))
				if err != nil {
					return err
				}
				if !found {
					return protocol.ErrAuth
				}
				view.Rows[i].Prefix, view.Rows[i].Status = f.height, ob.Status
			}
			if !view.BlockID.Equals(id) || !reflect.DeepEqual(view.Rows, answers) {
				return protocol.ErrAuth
			}
			// Use the actual retained commit, not a new signature on the revision.
			cert := f.original.LoadBlockCommit(1)
			originalParts, err := original.MakePartSet(types.BlockPartSizeBytes)
			if err != nil {
				return err
			}
			originalID := types.BlockID{Hash: original.Hash(), PartSetHeader: originalParts.Header()}
			revisedID := types.BlockID{Hash: b.Hash(), PartSetHeader: parts.Header()}
			for _, candidate := range []types.BlockID{originalID, revisedID} {
				if err = f.validators.VerifyCommitLight(chain, candidate, 1, cert); err != nil {
					return err
				}
			}
			// Isolate the role of part adaptation: this candidate already has
			// the valid input opening, but rebuilds parts with initial openings.
			plainParts, err := types.NewRedactablePartSet(revision.Body, types.BlockPartSizeBytes, 1, 0, nil)
			if err != nil {
				return err
			}
			plainID := types.BlockID{Hash: b.Hash(), PartSetHeader: plainParts.Header()}
			plainErr := f.validators.VerifyCommitLight(chain, plainID, 1, cert)
			if !bytes.Equal(plainID.Hash, id.Hash) || plainID.PartSetHeader.Equals(id.PartSetHeader) || plainErr == nil || !strings.Contains(plainErr.Error(), "wrong block ID") {
				return protocol.ErrAuth
			}
			out = exported{Prefix: f.height, Height: 1, BlockID: id, Revision: revision.Number, Task: f.batch.ID(), OriginalBody: raw, AuthorizedBody: revision.Body, HistoricalFunding: pay.Tx.Funding[0], Economic: answers[0]}
			out.IndexedEconomic, out.MaterializedEconomic = indexed[0], view.Rows[0]
			out.OriginalCommitVerified, out.RevisedCommitVerified, out.UnadaptedPartsRejected = true, true, true
			return nil
		}))
		return
	}
	before := read()
	savedView := bytes.Clone(materializedView)
	readerRecoveryCheck(t, f)
	after := read()
	if !bytes.Equal(before.AuthorizedBody, after.AuthorizedBody) || !bytes.Equal(savedView, materializedView) || before.Economic.Status != rules.DirectRepaired || after.Economic.Status != rules.DirectRecovered || before.Economic.Decision != after.Economic.Decision || before.Economic.Ref != after.Economic.Ref {
		t.Fatal("historical provenance and current repayment state conflated")
	}
	if path := os.Getenv("UTXO_HISTORY_EXPORT"); path != "" {
		raw, err := json.MarshalIndent([]exported{before, after}, "", "  ")
		readerOK(t, err)
		readerOK(t, os.WriteFile(path, raw, 0644))
	}
	t.Log("original-coordinate export verified: authorization, full BlockID, owner signatures, direct certificates, immutable outputs, and paid provenance after repayment")
	t.Log("same-prefix index/materialized-row/C3 economic answers agree; original and revised bodies reuse the retained commit; equal header with unadapted parts is rejected")
}
