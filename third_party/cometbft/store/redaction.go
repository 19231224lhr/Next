package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/types"
)

func revisionKey(height int64) []byte { return []byte(fmt.Sprintf("RV:%d", height)) }
func originalKey(height int64) []byte { return []byte(fmt.Sprintf("RO:%d", height)) }

// ReviseBlock writes the actual stored Part payloads in a single database batch.
// authorize must verify an already-finalized RepairInput and its exact byte diff.
// This method never changes account balances or rewrites historical AppHash.
func (bs *BlockStore) ReviseBlock(height int64, base uint64, parts *types.PartSet, authorize func(*types.Block, *types.Block) error) error {
	if base == ^uint64(0) {
		return types.ErrRedaction
	}
	return bs.installRevision(height, base+1, parts, authorize, true)
}

// InstallCommittedRevision installs an exact, already-authorized snapshot.
// The callback must use copied committed data, never acquire application locks.
func (bs *BlockStore) InstallCommittedRevision(height int64, revision uint64, parts *types.PartSet, authorize func(*types.Block, *types.Block) error) error {
	return bs.installRevision(height, revision, parts, authorize, false)
}

func (bs *BlockStore) installRevision(height int64, target uint64, parts *types.PartSet, authorize func(*types.Block, *types.Block) error, sequential bool) error {
	if target == 0 {
		return types.ErrRedaction
	}

	if parts == nil || !parts.IsComplete() || authorize == nil {
		return types.ErrRedaction
	}
	bs.revisionMtx.Lock()
	defer bs.revisionMtx.Unlock()
	meta := bs.LoadBlockMeta(height)
	if meta == nil || !parts.Header().Equals(meta.BlockID.PartSetHeader) {
		return types.ErrRedaction
	}
	data, err := io.ReadAll(parts.GetReader())
	if err != nil {
		return err
	}
	pb := new(cmtproto.Block)
	if err = pb.Unmarshal(data); err != nil {
		return err
	}
	next, err := types.BlockFromProto(pb)
	if err != nil {
		return err
	}
	if next.Height != height || !bytes.Equal(next.Hash(), meta.BlockID.Hash) {
		return types.ErrRedaction
	}
	current := bs.loadBlock(height)
	if err = authorize(current, next); err != nil {
		return err
	}
	state, err := bs.db.Get(revisionKey(height))
	if err != nil {
		return err
	}
	var rev uint64
	if len(state) != 0 {
		if len(state) != 40 {
			return types.ErrRedaction
		}
		rev = binary.BigEndian.Uint64(state[:8])
	}
	digest := sha256.Sum256(data)
	checked := types.NewPartSetFromHeader(meta.BlockID.PartSetHeader)
	for i := 0; i < int(parts.Total()); i++ {
		p := parts.GetPart(i)
		if p.Redaction == nil || p.Redaction.Height != height || p.Redaction.Revision != target {
			return types.ErrRedaction
		}
		if err = p.ValidateBasic(); err != nil {
			return err
		}
		if _, err = checked.AddPart(p); err != nil {
			return err
		}
	}

	if rev == target {
		if len(state) != 40 || !bytes.Equal(state[8:], digest[:]) {
			return types.ErrRedaction
		}
		for i := 0; i < int(parts.Total()); i++ {
			old := bs.LoadBlockPart(height, i)
			if old == nil || old.Redaction == nil || *old.Redaction != *parts.GetPart(i).Redaction || !bytes.Equal(old.Bytes, parts.GetPart(i).Bytes) {
				return types.ErrRedaction
			}
		}
		return nil
	}
	if !sequential && rev > target {
		return nil
	}
	if sequential && rev != target-1 {
		return types.ErrRedaction
	}
	batch := bs.db.NewBatch()
	defer batch.Close()
	if rev == 0 {
		origin, err := current.ToProto()
		if err != nil {
			return err
		}
		b, err := origin.Marshal()
		if err != nil {
			return err
		}
		if err = batch.Set(originalKey(height), b); err != nil {
			return err
		}
	}
	for i := 0; i < int(parts.Total()); i++ {
		part, err := parts.GetPart(i).ToProto()
		if err != nil {
			return err
		}
		b, err := part.Marshal()
		if err != nil {
			return err
		}
		if err = batch.Set(calcBlockPartKey(height, i), b); err != nil {
			return err
		}
	}
	state = make([]byte, 40)
	binary.BigEndian.PutUint64(state[:8], target)
	copy(state[8:], digest[:])
	if err = batch.Set(revisionKey(height), state); err != nil {
		return err
	}
	return batch.WriteSync()
}

// LoadOriginalBlock supplies the original execution bytes for deterministic
// replay. Compensation is applied only when replay reaches RepairInput's height.
func (bs *BlockStore) LoadOriginalBlock(height int64) (*types.Block, error) {
	bs.revisionMtx.RLock()
	defer bs.revisionMtx.RUnlock()
	b, err := bs.db.Get(originalKey(height))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return bs.loadBlock(height), nil
	}
	p := new(cmtproto.Block)
	if err = p.Unmarshal(b); err != nil {
		return nil, err
	}
	return types.BlockFromProto(p)
}

// OriginalBlockPart serves consensus catch-up with original execution bytes.
// Current redacted parts remain available through LoadBlockPart for inspection.
func (bs *BlockStore) OriginalBlockPart(height int64, index int) (*types.Part, error) {
	bs.revisionMtx.RLock()
	defer bs.revisionMtx.RUnlock()
	raw, err := bs.db.Get(originalKey(height))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		meta := bs.LoadBlockMeta(height)
		if meta == nil {
			return nil, nil
		}
		if index < 0 || uint64(index) >= uint64(meta.BlockID.PartSetHeader.Total) {
			return nil, types.ErrRedaction
		}
		return bs.LoadBlockPart(height, index), nil
	}
	// Rewritten history still serves the immutable original execution bytes.
	pb := new(cmtproto.Block)
	if err = pb.Unmarshal(raw); err != nil {
		return nil, err
	}
	block, err := types.BlockFromProto(pb)
	if err != nil {
		return nil, err
	}
	parts, err := block.MakePartSet(types.BlockPartSizeBytes)
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= int(parts.Total()) {
		return nil, types.ErrRedaction
	}
	return parts.GetPart(index), nil
}
