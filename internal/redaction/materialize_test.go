//go:build comet_v3

package redaction_test

import (
	"bytes"
	"crypto/sha256"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	cmtstore "github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
	"utxo/crypto/chameleon"
	"utxo/internal/redaction"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestMaterializationRevisionOrderAndCursorReplay(t *testing.T) {
	p, signers, vals, keys := committee(t)
	ctx := []byte("materialization-order")
	ref := bytes.Repeat([]byte{1}, 32)
	c, r, err := p.Commit(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	tx := types.EncodeRedactableTx(c[:], append(bytes.Clone(ref), r[:]...))
	b := block(t, 1, tx, &types.Commit{}, vals)
	parts, err := b.MakePartSet(types.BlockPartSizeBytes)
	if err != nil {
		t.Fatal(err)
	}
	id := types.BlockID{Hash: b.Hash(), PartSetHeader: parts.Header()}
	disk, err := dbm.NewDB("revisions", dbm.GoLevelDBBackend, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	blocks := cmtstore.NewBlockStore(disk)
	blocks.SaveBlock(b, parts, commit(t, 1, id, vals, keys))
	db := store.NewMemory()
	defer db.Close()
	// Deliberately reverse identity order; revision order must take precedence.
	ids := []protocol.Hash{{255}, {1}}
	if bytes.Compare(redaction.QueueKey(3, 1, 1, ids[0]), redaction.QueueKey(3, 1, 2, ids[1])) >= 0 {
		t.Fatal("same-block repairs sorted by identity instead of revision")
	}
	var finalBody []byte
	for round := 0; round < 2; round++ {
		nextRef := bytes.Repeat([]byte{byte(round + 2)}, 32)
		r = adapt(t, p, signers, ctx, ref, nextRef, c, r)
		pb, err := b.ToProto()
		if err != nil {
			t.Fatal(err)
		}
		pb.Data.Txs[0] = types.EncodeRedactableTx(c[:], append(bytes.Clone(nextRef), r[:]...))
		body, err := pb.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		openings := make([]chameleon.Opening, parts.Total())
		for i := range openings {
			old := parts.GetPart(i)
			pc := types.RedactionContext(1, uint32(i))
			h, err := p.Digest(pc, old.Bytes, old.Redaction.Opening)
			if err != nil {
				t.Fatal(err)
			}
			start, end := i*int(types.BlockPartSizeBytes), min((i+1)*int(types.BlockPartSizeBytes), len(body))
			openings[i] = adapt(t, p, signers, pc, old.Bytes, body[start:end], h, old.Redaction.Opening)
		}
		command := protocol.RepairInput{Height: 1, Base: uint64(round), Next: protocol.Hash(sha256.Sum256(body)), Parts: openings}
		if err := db.Update(func(v state.ReadView) ([]state.Change, error) {
			o := state.NewOverlay(v)
			if err := state.Put(o, redaction.TaskKey(ids[round]), redaction.Task{Command: command, Body: body, Height: 3}); err != nil {
				return nil, err
			}
			if err := state.Put(o, redaction.RevisionKey(1), redaction.Revision{Number: uint64(round + 1), Body: body}); err != nil {
				return nil, err
			}
			return o.Changes(), nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.View(func(v state.ReadView) error { return redaction.Materialize(v, blocks, ids[round]) }); err != nil {
			t.Fatal(err)
		}
		parts, err = types.NewRedactablePartSet(body, types.BlockPartSizeBytes, 1, uint64(round+1), openings)
		if err != nil {
			t.Fatal(err)
		}
		b, err = types.BlockFromProto(pb)
		if err != nil {
			t.Fatal(err)
		}
		ref, finalBody = nextRef, body
	}
	// Re-reading both tasks simulates a missing local cursor after normal replay.
	for _, id := range ids {
		if err := db.View(func(v state.ReadView) error { return redaction.Materialize(v, blocks, id) }); err != nil {
			t.Fatal(err)
		}
	}
	actual, err := blocks.LoadBlock(1).ToProto()
	if err != nil {
		t.Fatal(err)
	}
	wire, err := actual.Marshal()
	if err != nil || !bytes.Equal(wire, finalBody) {
		t.Fatal("older task reverted current history")
	}
}
