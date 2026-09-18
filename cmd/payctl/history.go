//go:build comet_v3

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	dbm "github.com/cometbft/cometbft-db"
	cmtstore "github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/redaction"
	"utxo/internal/state"
	"utxo/protocol"
)

func auditDirectHistory(v state.ReadView, n cfg.Network, dir string) (int, error) {
	p, err := n.Direct.Policy(n.Schedule, n.Organizations)
	if err != nil {
		return 0, err
	}
	if err = types.ConfigureRedaction(n.ChainID, p.Key); err != nil {
		return 0, err
	}
	trust, err := n.Trust()
	if err != nil {
		return 0, err
	}
	db, err := dbm.NewDB("blockstore", dbm.GoLevelDBBackend, dir)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	blocks := cmtstore.NewBlockStore(db)
	scanner := v.(state.ScanView)
	var cursor []byte
	count := 0
	for {
		entries, err := scanner.Scan(state.Key(108), cursor, 128)
		if err != nil {
			return count, err
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			d := protocol.NewDecoder(entry.Key)
			d.U16()
			d.U8()
			number := protocol.NewDecoder(d.Bytes(8))
			height := int64(number.U64())
			if number.Done() != nil || d.Done() != nil {
				return count, protocol.ErrEncoding
			}
			var revision redaction.Revision
			if err = json.Unmarshal(entry.Value, &revision); err != nil {
				return count, err
			}
			b := blocks.LoadBlock(height)
			if b == nil {
				return count, fmt.Errorf("missing revised block")
			}
			pb, err := b.ToProto()
			if err != nil {
				return count, err
			}
			raw, err := pb.Marshal()
			if err != nil {
				return count, err
			}
			if !bytes.Equal(raw, revision.Body) {
				return count, fmt.Errorf("revision %d not materialized", height)
			}
			meta := blocks.LoadBlockMeta(height)
			commit := blocks.LoadBlockCommit(height)
			if commit == nil {
				commit = blocks.LoadSeenCommit(height)
			}
			if err = trust.Validators.VerifyCommitLight(n.ChainID, meta.BlockID, height, commit); err != nil {
				return count, err
			}
			command, found, err := state.Load[protocol.RepairInput](v, state.Key(110, redaction.RevisionKey(height)))
			if err != nil || !found {
				return count, fmt.Errorf("missing exact repair authorization")
			}
			parts, err := types.NewRedactablePartSet(raw, types.BlockPartSizeBytes, height, revision.Number, command.Parts)
			if err != nil {
				return count, err
			}
			if !parts.Header().Equals(meta.BlockID.PartSetHeader) || !bytes.Equal(b.Hash(), meta.BlockID.Hash) {
				return count, fmt.Errorf("historical BlockID changed")
			}
			original, err := blocks.LoadOriginalBlock(height)
			if err != nil {
				return count, err
			}
			if original == nil {
				return count, fmt.Errorf("missing replay body")
			}
			count++
			cursor = entry.Key
		}
	}
	return count, nil
}
