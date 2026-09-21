package committee

import (
	"bytes"
	"context"
	"errors"
	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"path/filepath"
	"testing"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
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
	execute := func(v state.ReadView, tx []byte) (state.Transition, error) {
		if tx[0] == 0 {
			return state.Transition{}, errors.New("business reject")
		}
		return state.Transition{Changes: []state.Change{{Key: tx[:1], Value: tx[1:]}}}, nil
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
	}, func(state.ReadView, []byte) (state.Transition, error) { panic("proposal executed") })
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

func TestFactProofMaterialOnlyAfterCommit(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	network := protocol.Digest("NETWORK", []byte("network"))
	fact := protocol.FinalFact{Kind: protocol.FactOutputCreated, Key: protocol.Digest("output", nil), Revision: 1, Network: network, Payload: []byte("created")}
	app, e := NewApp("network", db, func([]byte) error { return nil }, func(state.ReadView, []byte) (state.Transition, error) {
		return state.Transition{Facts: []protocol.FinalFact{fact}}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	response, e := app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: 1, Hash: bytes.Repeat([]byte{1}, 32), Txs: [][]byte{{1}}})
	if e != nil {
		t.Fatal(e)
	}
	header := cmttypes.SignedHeader{Header: &cmttypes.Header{Height: 2, AppHash: response.AppHash}}
	if _, e := app.Proof(fact.ID(), header); e == nil {
		t.Fatal("uncommitted proof")
	}
	if _, e = app.Commit(context.Background(), &abci.RequestCommit{}); e != nil {
		t.Fatal(e)
	}
	proof, e := app.Proof(fact.ID(), header)
	if e != nil {
		t.Fatal(e)
	}
	leaf, _ := fact.MarshalBinary()
	if e = proof.Path.Verify(proof.Commitment.FactRoot, leaf); e != nil {
		t.Fatal(e)
	}
	root, _ := proof.Commitment.Hash()
	if !bytes.Equal(root[:], response.AppHash) {
		t.Fatal("root mismatch")
	}
}

func TestBusinessSchemaPrefixDoesNotOverlapLocalMetadata(t *testing.T) {
	for _, key := range [][]byte{state.Key(state.KeyAccount, []byte("account")), []byte{0xff, 'p', 1}} {
		db := store.NewMemory()
		app, e := NewApp("network", db, func([]byte) error { return nil }, func(state.ReadView, []byte) (state.Transition, error) {
			return state.Transition{Changes: []state.Change{{Key: key, Value: []byte{1}}}}, nil
		})
		if e != nil {
			t.Fatal(e)
		}
		_, e = app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: 1, Hash: bytes.Repeat([]byte{1}, 32), Txs: [][]byte{{1}}})
		if key[0] == 0xff {
			if e == nil {
				t.Fatal("metadata write allowed")
			}
		} else {
			if e != nil {
				t.Fatalf("business schema rejected: %v", e)
			}
		}
		db.Close()
	}
}
