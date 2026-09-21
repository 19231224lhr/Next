package committee

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	bolt "go.etcd.io/bbolt"
	"utxo/internal/state"
	"utxo/internal/store"
)

func TestDatabasePageSizePreservesCommittedLedger(t *testing.T) {
	cfg, _, _, txs := cacheFixture(t, 96)
	var reference []state.Entry
	var responses []*abci.ResponseFinalizeBlock
	for _, pageSize := range []int{16384, 4096} {
		t.Run(fmt.Sprint(pageSize), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "committee.db")
			physical, err := bolt.Open(path, 0600, &bolt.Options{PageSize: pageSize})
			if err != nil {
				t.Fatal(err)
			}
			if err = physical.Close(); err != nil {
				t.Fatal(err)
			}
			id := store.Identity{Network: cfg.Network.String(), Role: "committee", Node: "0", Schema: 4}
			db, err := store.Open(path, id)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { db.Close() }()
			for height := int64(1); height <= 3; height++ {
				// Recreate the application after each durable block, as on restart.
				engine, err := NewEngine(cfg, db)
				if err != nil {
					t.Fatal(err)
				}
				app, err := NewTimedApp("verify-cache", db, engine.Check, engine.ExecuteAt, engine.BeginBlock)
				if err != nil {
					t.Fatal(err)
				}
				result, err := app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{
					Height: height, Hash: bytes.Repeat([]byte{byte(height)}, 32), Time: time.Unix(100+height, 0), Txs: txs[(height-1)*32 : height*32],
				})
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range result.TxResults {
					if r.Code != 0 {
						t.Fatalf("execution rejected: %v", r)
					}
				}
				if _, err = app.Commit(context.Background(), &abci.RequestCommit{}); err != nil {
					t.Fatal(err)
				}
				if pageSize == 16384 {
					responses = append(responses, result)
				} else if !reflect.DeepEqual(result, responses[height-1]) {
					t.Fatal("page layout changed execution response or AppHash")
				}
				if err = db.Close(); err != nil {
					t.Fatal(err)
				}
				db, err = store.Open(path, id)
				if err != nil {
					t.Fatal(err)
				}
			}
			ledger := cacheLedger(t, db)
			if pageSize == 16384 {
				reference = ledger
			} else if !reflect.DeepEqual(reference, ledger) {
				t.Fatal("page layout changed durable logical state")
			}
		})
	}
}
