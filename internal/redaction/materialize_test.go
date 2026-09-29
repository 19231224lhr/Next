//go:build comet_v3

package redaction_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
	for _, delayed := range []bool{false, true} {
		t.Run(fmt.Sprintf("delayed=%v", delayed), func(t *testing.T) { testMaterializationOrder(t, delayed) })
	}
}

func testMaterializationOrder(t *testing.T, delayed bool) {
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
	original, err := b.ToProto()
	if err != nil {
		t.Fatal(err)
	}
	originalBody, err := original.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var stale redaction.Materialization
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
			if err := state.Put(o, state.Key(110, redaction.RevisionKey(1)), command); err != nil {
				return nil, err
			}
			return o.Changes(), nil
		}); err != nil {
			t.Fatal(err)
		}
		if round == 0 {
			if err := db.View(func(v state.ReadView) error {
				var e error
				stale, e = redaction.PrepareMaterialization(v, ids[0])
				return e
			}); err != nil {
				t.Fatal(err)
			}
		}
		if !delayed {
			if err := db.View(func(v state.ReadView) error { return redaction.Materialize(v, blocks, ids[round]) }); err != nil {
				t.Fatal(err)
			}
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
	if delayed {
		if err := db.View(func(v state.ReadView) error { return redaction.Materialize(v, blocks, ids[0]) }); err != nil {
			t.Fatal("direct committed revision installation", err)
		}
	}
	if err := stale.Install(blocks); err != nil {
		t.Fatal("stale snapshot replay", err)
	}
	if part := blocks.LoadBlockPart(1, 0); part.Redaction.Revision != 2 {
		t.Fatal("latest committed revision was not installed")
	}
	restored, err := blocks.LoadOriginalBlock(1)
	if err != nil {
		t.Fatal(err)
	}
	restoredPB, err := restored.ToProto()
	if err != nil {
		t.Fatal(err)
	}
	restoredBody, err := restoredPB.Marshal()
	if err != nil || !bytes.Equal(restoredBody, originalBody) {
		t.Fatal("original execution history changed", err)
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

type countedRevisionDB struct {
	dbm.DB
	writes int
}
type countedRevisionBatch struct {
	dbm.Batch
	parent *countedRevisionDB
}

func (d *countedRevisionDB) NewBatch() dbm.Batch {
	return &countedRevisionBatch{Batch: d.DB.NewBatch(), parent: d}
}
func (b *countedRevisionBatch) WriteSync() error { b.parent.writes++; return b.Batch.WriteSync() }

func TestMaterializationInterleavedQueue(t *testing.T) {
	p, signers, vals, keys := committee(t)
	disk := &countedRevisionDB{DB: dbm.NewMemDB()}
	defer disk.Close()
	blocks := cmtstore.NewBlockStore(disk)
	db := store.NewMemory()
	defer db.Close()
	bodies := make(map[int64]*types.Block)
	partsByHeight := make(map[int64]*types.PartSet)
	refs := make(map[int64][]byte)
	openings := make(map[int64]chameleon.Opening)
	commitments := make(map[int64]chameleon.Commitment)
	for h := int64(1); h <= 2; h++ {
		refs[h] = bytes.Repeat([]byte{1}, 32)
		context := []byte(fmt.Sprint("interleaved-", h))
		c, r, e := p.Commit(context, refs[h])
		if e != nil {
			t.Fatal(e)
		}
		commitments[h], openings[h] = c, r
		b := block(t, h, types.EncodeRedactableTx(c[:], append(bytes.Clone(refs[h]), r[:]...)), &types.Commit{}, vals)
		parts, e := b.MakePartSet(types.BlockPartSizeBytes)
		if e != nil {
			t.Fatal(e)
		}
		id := types.BlockID{Hash: b.Hash(), PartSetHeader: parts.Header()}
		blocks.SaveBlock(b, parts, commit(t, h, id, vals, keys))
		bodies[h], partsByHeight[h] = b, parts
	}
	revisions := map[int64]uint64{}
	for i, h := range []int64{1, 2, 1} {
		base := revisions[h]
		revisions[h]++
		nextRef := bytes.Repeat([]byte{byte(i + 2)}, 32)
		context := []byte(fmt.Sprint("interleaved-", h))
		openings[h] = adapt(t, p, signers, context, refs[h], nextRef, commitments[h], openings[h])
		refs[h] = nextRef
		pb, e := bodies[h].ToProto()
		if e != nil {
			t.Fatal(e)
		}
		c, r := commitments[h], openings[h]
		pb.Data.Txs[0] = types.EncodeRedactableTx(c[:], append(bytes.Clone(nextRef), r[:]...))
		body, e := pb.Marshal()
		if e != nil {
			t.Fatal(e)
		}
		parts := partsByHeight[h]
		adapted := make([]chameleon.Opening, parts.Total())
		for j := range adapted {
			old := parts.GetPart(j)
			context := types.RedactionContext(h, uint32(j))
			commitment, e := p.Digest(context, old.Bytes, old.Redaction.Opening)
			if e != nil {
				t.Fatal(e)
			}
			start, end := j*int(types.BlockPartSizeBytes), min((j+1)*int(types.BlockPartSizeBytes), len(body))
			adapted[j] = adapt(t, p, signers, context, old.Bytes, body[start:end], commitment, old.Redaction.Opening)
		}
		command := protocol.RepairInput{Height: h, Base: base, Next: protocol.Hash(sha256.Sum256(body)), Parts: adapted}
		id := protocol.Hash{byte(i + 1)}
		if e = db.Update(func(v state.ReadView) ([]state.Change, error) {
			o := state.NewOverlay(v)
			for _, x := range []struct {
				key   []byte
				value any
			}{
				{redaction.TaskKey(id), redaction.Task{Command: command, Body: body, Height: int64(i + 3)}},
				{redaction.RevisionKey(h), redaction.Revision{Number: base + 1, Body: body}},
				{state.Key(110, redaction.RevisionKey(h)), command},
				{redaction.QueueKey(int64(i+3), h, base+1, id), id},
			} {
				if e := state.Put(o, x.key, x.value); e != nil {
					return nil, e
				}
			}
			return o.Changes(), nil
		}); e != nil {
			t.Fatal(e)
		}
		bodies[h], e = types.BlockFromProto(pb)
		if e != nil {
			t.Fatal(e)
		}
		partsByHeight[h], e = types.NewRedactablePartSet(body, types.BlockPartSizeBytes, h, base+1, adapted)
		if e != nil {
			t.Fatal(e)
		}
	}
	disk.writes = 0
	var cursor []byte
	for i, want := range []int64{1, 2, 1} {
		entries, e := store.Scan(db, state.Key(113), cursor, 1)
		if e != nil || len(entries) != 1 {
			t.Fatal("queue entry", e)
		}
		var id protocol.Hash
		if e = json.Unmarshal(entries[0].Value, &id); e != nil || id != (protocol.Hash{byte(i + 1)}) {
			t.Fatal("queue order changed", e)
		}
		var installation redaction.Materialization
		if e = db.View(func(v state.ReadView) error {
			var e error
			installation, e = redaction.PrepareMaterialization(v, id)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		if e = installation.Install(blocks); e != nil {
			t.Fatal(e)
		}
		cursor = entries[0].Key
		if blocks.LoadBlockPart(want, 0).Redaction.Revision != revisions[want] {
			t.Fatal("missed latest revision")
		}
	}
	if disk.writes != 2 {
		t.Fatalf("wanted two physical installations for A1/B1/A2, got %d", disk.writes)
	}
}
