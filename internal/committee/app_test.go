package committee

import (
	"bytes"
	"context"
	"errors"
	abci "github.com/cometbft/cometbft/abci/types"
	"path/filepath"
	"testing"
	"utxo/internal/state"
	"utxo/internal/store"
)

func TestP01P04CommitBoundaryAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	id := store.Identity{Network: "network", Role: "committee", Node: "0", Schema: 2}
	db, e := store.Open(path, id)
	if e != nil {
		t.Fatal(e)
	}
	check := func(tx []byte) error {
		if len(tx) != 2 {
			return errors.New("bad command")
		}
		return nil
	}
	execute := func(v state.ReadView, tx []byte) ([]state.Change, error) {
		if tx[0] == 0 {
			return nil, errors.New("business reject")
		}
		return []state.Change{{Key: tx[:1], Value: tx[1:]}}, nil
	}
	app, e := NewApp("network", db, check, execute)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	req := &abci.RequestFinalizeBlock{Height: 1, Hash: bytes.Repeat([]byte{1}, 32), Txs: [][]byte{{1, 2}, {0, 3}}}
	r, e := app.FinalizeBlock(ctx, req)
	if e != nil || r.TxResults[0].Code != 0 || r.TxResults[1].Code == 0 {
		t.Fatalf("%+v %v", r, e)
	}
	q, _ := app.Query(ctx, &abci.RequestQuery{Data: []byte{1}})
	if q.Code == 0 {
		t.Fatal("uncommitted visible")
	}
	info, _ := app.Info(ctx, &abci.RequestInfo{})
	if info.LastBlockHeight != 0 {
		t.Fatal("uncommitted height")
	}
	if _, e = app.Commit(ctx, &abci.RequestCommit{}); e != nil {
		t.Fatal(e)
	}
	q, _ = app.Query(ctx, &abci.RequestQuery{Data: []byte{1}})
	if q.Code != 0 || !bytes.Equal(q.Value, []byte{2}) {
		t.Fatal("missing commit")
	}
	root := bytes.Clone(r.AppHash)
	db.Close()
	db, e = store.Open(path, id)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	app, e = NewApp("network", db, check, execute)
	if e != nil {
		t.Fatal(e)
	}
	info, _ = app.Info(ctx, &abci.RequestInfo{})
	if info.LastBlockHeight != 1 || !bytes.Equal(info.LastBlockAppHash, root) {
		t.Fatal("reopen")
	}
	r, e = app.FinalizeBlock(ctx, req)
	if e != nil || !bytes.Equal(r.AppHash, root) {
		t.Fatal("replay")
	}
	req2 := &abci.RequestFinalizeBlock{Height: 2, Hash: bytes.Repeat([]byte{2}, 32)}
	r, e = app.FinalizeBlock(ctx, req2)
	if e != nil || !bytes.Equal(r.AppHash, root) {
		t.Fatal("empty block changed root")
	}
	app.Commit(ctx, &abci.RequestCommit{})
	req2.Hash[0] = 3
	if _, e = app.FinalizeBlock(ctx, req2); e == nil {
		t.Fatal("different block overwrote committed height")
	}
}
func TestProposalNeverMutatesAndRejectsMalformed(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	app, e := NewApp("network", db, func(b []byte) error {
		if len(b) != 1 {
			return errors.New("invalid")
		}
		return nil
	}, func(state.ReadView, []byte) ([]state.Change, error) { panic("proposal executed") })
	if e != nil {
		t.Fatal(e)
	}
	r, e := app.ProcessProposal(context.Background(), &abci.RequestProcessProposal{Txs: [][]byte{{1, 2}}})
	if e != nil || r.Status != abci.ResponseProcessProposal_REJECT {
		t.Fatal("malformed accepted")
	}
	r, e = app.ProcessProposal(context.Background(), &abci.RequestProcessProposal{Txs: [][]byte{{1}}})
	if e != nil || r.Status != abci.ResponseProcessProposal_ACCEPT {
		t.Fatal("valid rejected")
	}
}
