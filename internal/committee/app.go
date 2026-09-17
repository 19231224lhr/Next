package committee

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/crypto/merkle"
	"sync"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

var ErrBlock = errors.New("inconsistent block height or identity")
var metaKey = []byte{0, 2, 'c', 'o', 'm', 'm', 'i', 't'}

type CheckFunc func([]byte) error
type ExecuteFunc func(state.ReadView, []byte) ([]state.Change, error)
type commitRecord struct {
	Height   int64
	BlockID  []byte
	Response []byte
}

// App provides ABCI commit isolation. Business execution is supplied by the
// deterministic payment rules; no standalone node accepts arbitrary key writes.
type App struct {
	abci.BaseApplication
	mu             sync.Mutex
	chain          string
	db             store.Store
	check          CheckFunc
	execute        ExecuteFunc
	committed      commitRecord
	response       *abci.ResponseFinalizeBlock
	pending        *commitRecord
	pendingChanges []state.Change
	halted         error
}

var _ abci.Application = (*App)(nil)

func NewApp(chain string, db store.Store, check CheckFunc, execute ExecuteFunc) (*App, error) {
	if chain == "" || db == nil || check == nil || execute == nil {
		return nil, protocol.ErrRule
	}
	a := &App{chain: chain, db: db, check: check, execute: execute, response: &abci.ResponseFinalizeBlock{}}
	e := db.View(func(v state.ReadView) error {
		raw, e := v.Get(metaKey)
		if errors.Is(e, state.ErrNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		if e = json.Unmarshal(raw, &a.committed); e != nil {
			return e
		}
		return a.response.Unmarshal(a.committed.Response)
	})
	if e != nil {
		return nil, e
	}
	return a, nil
}
func (a *App) Info(context.Context, *abci.RequestInfo) (*abci.ResponseInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return &abci.ResponseInfo{Version: "utxo-v2", AppVersion: 2, LastBlockHeight: a.committed.Height, LastBlockAppHash: bytes.Clone(a.response.AppHash)}, nil
}
func (a *App) InitChain(_ context.Context, r *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
	if r.ChainId != a.chain || r.InitialHeight > 1 {
		return nil, ErrBlock
	}
	return &abci.ResponseInitChain{}, nil
}
func (a *App) CheckTx(_ context.Context, r *abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
	if e := a.check(r.Tx); e != nil {
		return &abci.ResponseCheckTx{Code: 1, Log: e.Error()}, nil
	}
	return &abci.ResponseCheckTx{}, nil
}
func (a *App) PrepareProposal(_ context.Context, r *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
	out := &abci.ResponsePrepareProposal{}
	var size int64
	for _, tx := range r.Txs {
		if a.check(tx) != nil {
			continue
		}
		if int64(len(tx)) > r.MaxTxBytes-size {
			break
		}
		size += int64(len(tx))
		out.Txs = append(out.Txs, bytes.Clone(tx))
	}
	return out, nil
}
func (a *App) ProcessProposal(_ context.Context, r *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	for _, tx := range r.Txs {
		if a.check(tx) != nil {
			return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
		}
	}
	return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_ACCEPT}, nil
}
func cloneResponse(r *abci.ResponseFinalizeBlock) *abci.ResponseFinalizeBlock {
	b, _ := r.Marshal()
	c := new(abci.ResponseFinalizeBlock)
	_ = c.Unmarshal(b)
	return c
}
func (a *App) FinalizeBlock(_ context.Context, r *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.halted != nil {
		return nil, a.halted
	}
	if r.Height == a.committed.Height && bytes.Equal(r.Hash, a.committed.BlockID) {
		return cloneResponse(a.response), nil
	}
	if r.Height != a.committed.Height+1 || len(r.Hash) != 32 {
		return nil, ErrBlock
	}
	if a.pending != nil {
		if r.Height != a.pending.Height || !bytes.Equal(r.Hash, a.pending.BlockID) {
			return nil, ErrBlock
		}
		out := new(abci.ResponseFinalizeBlock)
		if e := out.Unmarshal(a.pending.Response); e != nil {
			return nil, e
		}
		return out, nil
	}
	response := &abci.ResponseFinalizeBlock{TxResults: make([]*abci.ExecTxResult, len(r.Txs)), AppHash: bytes.Clone(a.response.AppHash)}
	var changes []state.Change
	e := a.db.View(func(v state.ReadView) error {
		overlay := state.NewOverlay(v)
		for i, tx := range r.Txs {
			response.TxResults[i] = &abci.ExecTxResult{}
			if err := a.check(tx); err != nil {
				response.TxResults[i].Code = 1
				response.TxResults[i].Log = "invalid command"
				continue
			}
			cs, err := a.execute(overlay, tx)
			if err != nil {
				response.TxResults[i].Code = 2
				response.TxResults[i].Log = "business rejected"
				continue
			}
			for _, c := range cs {
				if len(c.Key) == 0 || c.Key[0] == 0 {
					return errors.New("executor used reserved state key")
				}
			}
			overlay.Apply(cs)
		}
		changes = overlay.Changes()
		return nil
	})
	if e != nil {
		return nil, e
	}
	if len(changes) > 0 {
		enc := new(protocol.Encoder)
		for _, c := range changes {
			enc.Bytes(c.Key)
			enc.Optional(c.Delete)
			enc.Bytes(c.Value)
		}
		writeHash := protocol.Digest("WRITE_SET", enc.Data())
		height := new(protocol.Encoder)
		height.U64(uint64(r.Height))
		root := protocol.Digest("APP_V2", []byte(a.chain), height.Data(), a.response.AppHash, writeHash[:], merkle.HashFromByteSlices(nil))
		response.AppHash = bytes.Clone(root[:])
	}
	raw, e := response.Marshal()
	if e != nil {
		return nil, e
	}
	a.pending = &commitRecord{Height: r.Height, BlockID: bytes.Clone(r.Hash), Response: raw}
	a.pendingChanges = changes
	return cloneResponse(response), nil
}
func (a *App) Commit(context.Context, *abci.RequestCommit) (*abci.ResponseCommit, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.halted != nil {
		return nil, a.halted
	}
	if a.pending == nil {
		return &abci.ResponseCommit{}, nil
	}
	raw, e := json.Marshal(a.pending)
	if e != nil {
		return nil, e
	}
	cs := append([]state.Change(nil), a.pendingChanges...)
	cs = append(cs, state.Change{Key: metaKey, Value: raw})
	e = a.db.Update(func(state.ReadView) ([]state.Change, error) { return cs, nil })
	if e != nil {
		a.halted = e
		return nil, e
	}
	a.committed = *a.pending
	a.response = new(abci.ResponseFinalizeBlock)
	if e = a.response.Unmarshal(a.pending.Response); e != nil {
		a.halted = e
		return nil, e
	}
	a.pending = nil
	a.pendingChanges = nil
	return &abci.ResponseCommit{}, nil
}
func (a *App) Query(_ context.Context, r *abci.RequestQuery) (*abci.ResponseQuery, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := &abci.ResponseQuery{Height: a.committed.Height}
	if len(r.Data) == 0 || r.Data[0] == 0 {
		return &abci.ResponseQuery{Code: 1, Log: "invalid key"}, nil
	}
	e := a.db.View(func(v state.ReadView) error {
		b, e := v.Get(r.Data)
		if errors.Is(e, state.ErrNotFound) {
			out.Code = 1
			return nil
		}
		if e != nil {
			return e
		}
		out.Value = b
		return nil
	})
	if e != nil {
		return nil, fmt.Errorf("query committed state: %w", e)
	}
	return out, nil
}
func (a *App) OfferSnapshot(context.Context, *abci.RequestOfferSnapshot) (*abci.ResponseOfferSnapshot, error) {
	return &abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_REJECT}, nil
}
func (a *App) ApplySnapshotChunk(context.Context, *abci.RequestApplySnapshotChunk) (*abci.ResponseApplySnapshotChunk, error) {
	return &abci.ResponseApplySnapshotChunk{Result: abci.ResponseApplySnapshotChunk_ABORT}, nil
}
