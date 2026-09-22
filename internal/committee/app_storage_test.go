package committee

import (
	"bytes"
	"context"
	abci "github.com/cometbft/cometbft/abci/types"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"utxo/internal/state"
	"utxo/internal/store"
)

// Both backends must publish exactly the same committed payment ledger,
// including fees, deduplication and block metadata. Finalize is not Commit.
func TestDirectApplicationStorageEquivalence(t *testing.T) {
	cfg, _, _, txs := cacheFixture(t, 9)
	blocks := [][][]byte{txs[:4], {txs[0], txs[4], txs[5]}, txs[6:], nil}
	var reference [][]state.Entry
	var hashes [][]byte
	for _, mode := range []string{"bbolt", "ephemeral"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "committee.db")
			id := store.Identity{Network: cfg.Network.String(), Role: "committee", Node: "0", Schema: 4}
			var db store.Store
			var err error
			if mode == "bbolt" {
				db, err = store.Open(path, id)
			} else {
				db, err = store.OpenEphemeral(path, id)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			engine, err := NewEngine(cfg, db)
			if err != nil {
				t.Fatal(err)
			}
			app, err := NewTimedApp("storage-test", db, engine.Check, engine.ExecuteAt, engine.BeginBlock)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			for i, txs := range blocks {
				before := cacheLedger(t, db)
				req := &abci.RequestFinalizeBlock{Height: int64(i + 1), Hash: bytes.Repeat([]byte{byte(i + 1)}, 32), Time: time.Unix(int64(100+i), 0), Txs: txs}
				response, err := app.FinalizeBlock(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				for _, result := range response.TxResults {
					if result.Code != 0 {
						t.Fatal(result.Log)
					}
				}
				if !reflect.DeepEqual(before, cacheLedger(t, db)) {
					t.Fatal("Finalize exposed uncommitted state")
				}
				info, err := app.Info(ctx, &abci.RequestInfo{})
				if err != nil || info.LastBlockHeight != int64(i) {
					t.Fatal("height published before Commit", info, err)
				}
				if _, err = app.Commit(ctx, &abci.RequestCommit{}); err != nil {
					t.Fatal(err)
				}
				ledger := cacheLedger(t, db)
				if mode == "bbolt" {
					reference = append(reference, ledger)
					hashes = append(hashes, bytes.Clone(response.AppHash))
				} else if !reflect.DeepEqual(reference[i], ledger) || !bytes.Equal(hashes[i], response.AppHash) {
					t.Fatal("backend changed ledger or AppHash at height", i+1)
				}
			}
			final := cacheLedger(t, db)
			if err = db.Close(); err != nil {
				t.Fatal("audit export", err)
			}
			var exported []state.Entry
			if err = store.Inspect(path, func(v state.ReadView) error {
				scan := v.(state.ScanView)
				var after []byte
				for {
					rows, e := scan.Scan(nil, after, 1024)
					if e != nil {
						return e
					}
					if len(rows) == 0 {
						return nil
					}
					exported = append(exported, rows...)
					after = rows[len(rows)-1].Key
				}
			}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(final, exported) {
				t.Fatal("audit export changed state")
			}
		})
	}
}
