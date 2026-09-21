package blockfollow_test

import (
	"context"
	"errors"
	"testing"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
)

type oneBlock struct{ p finality.BlockData }

func (s oneBlock) Block(ctx context.Context, height int64) (finality.BlockData, error) {
	if err := ctx.Err(); err != nil {
		return finality.BlockData{}, err
	}
	if height != 1 {
		return finality.BlockData{}, finality.ErrProof
	}
	return s.p, nil
}
func TestPublishHeightOnlyAfterCommit(t *testing.T) {
	for _, fail := range []bool{false, true} {
		db := store.NewMemory()
		ctx, cancel := context.WithCancel(context.Background())
		trust, p := testkit.Block("published", 1, nil, nil, nil)
		published := false
		boom := errors.New("commit failed")
		err := blockfollow.Run(ctx, db, oneBlock{p}, trust, func(finality.VerifiedBlock) (blockfollow.Apply, error) {
			return func(o *state.Overlay) error {
				if published {
					t.Fatal("published before apply")
				}
				if fail {
					return boom
				}
				return nil
			}, nil
		}, func(h int64) {
			got, e := blockfollow.Height(db)
			if e != nil || got != h || h != 1 {
				t.Fatal("published before cursor commit", got, h, e)
			}
			published = true
			cancel()
		})
		cancel()
		db.Close()
		if fail {
			if published || !errors.Is(err, boom) {
				t.Fatal("published failed commit", err)
			}
		} else if err != nil || !published {
			t.Fatal("missing successful commit", err)
		}
	}
}
