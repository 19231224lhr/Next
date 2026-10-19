package committee

import (
	"bytes"
	"context"
	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"testing"
	"utxo/internal/state"
	"utxo/internal/store"
)

func TestFinalC3ProposalRespectsProtoFraming(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	app, e := NewApp("framing", db, func([]byte) error { return nil }, func(state.ReadView, []byte) (state.Transition, error) { return state.Transition{}, nil })
	if e != nil {
		t.Fatal(e)
	}
	tx := bytes.Repeat([]byte{1}, 127)
	result, e := app.PrepareProposal(context.Background(), &abci.RequestPrepareProposal{Txs: [][]byte{tx}, MaxTxBytes: 128})
	if e != nil {
		t.Fatal(e)
	}
	var txs []cmttypes.Tx
	for _, tx := range result.Txs {
		txs = append(txs, tx)
	}
	if n := cmttypes.ComputeProtoSizeForTxs(txs); n > 128 {
		t.Fatalf("raw-fit proposal exceeds consensus frame bound: %d", n)
	}
}
