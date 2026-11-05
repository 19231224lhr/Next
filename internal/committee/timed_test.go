package committee

import (
	"bytes"
	"context"
	abci "github.com/cometbft/cometbft/abci/types"
	"testing"
	"time"
	"utxo/internal/state"
	"utxo/internal/store"
)

func TestBlockContextUsesConsensusTime(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	var seen BlockContext
	app, err := NewTimedApp("timed", db, func([]byte) error { return nil }, func(_ state.ReadView, _ []byte, b BlockContext) (state.Transition, error) {
		seen = b
		return state.Transition{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(123456, 0).UTC()
	r, err := app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: 1, Hash: bytes.Repeat([]byte{1}, 32), Time: stamp, Txs: [][]byte{{1}}})
	if err != nil || r.TxResults[0].Code != 0 || seen.Height != 1 || seen.Index != 0 || !seen.Time.Equal(stamp) {
		t.Fatalf("wrong block context: %+v %v", seen, err)
	}
}
