package committee

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/crypto/merkle"
	cmttypes "github.com/cometbft/cometbft/types"
	"sort"
	"sync"
	"time"
	"utxo/finality"
	"utxo/internal/requesttrace"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

var ErrBlock = errors.New("inconsistent block height or identity")
var metaKey = []byte{0xff, 2, 'c', 'o', 'm', 'm', 'i', 't'}

type MaintenanceFunc func(state.ReadView) (state.Transition, error)
type CheckFunc func([]byte) error
type ExecuteFunc func(state.ReadView, []byte) (state.Transition, error)
type BlockContext struct {
	Height int64
	Index  uint32
	Time   time.Time
}
type TimedExecuteFunc func(state.ReadView, []byte, BlockContext) (state.Transition, error)
type BeginBlockFunc func(state.ReadView, BlockContext) (state.Transition, error)
type commitRecord struct {
	Commitment finality.Commitment
	Leaves     [][]byte
	Height     int64
	BlockID    []byte
	Response   []byte
}

// App provides ABCI commit isolation. Business execution is supplied by the
// deterministic payment rules; no standalone node accepts arbitrary key writes.
type App struct {
	maintenance MaintenanceFunc
	abci.BaseApplication
	mu             sync.Mutex
	chain          string
	db             store.Store
	check          CheckFunc
	execute        ExecuteFunc
	executeAt      TimedExecuteFunc
	beginBlock     BeginBlockFunc
	committed      commitRecord
	response       *abci.ResponseFinalizeBlock
	pending        *commitRecord
	pendingChanges []state.Change
	halted         error
}

var _ abci.Application = (*App)(nil)

// NewTimedApp supplies committed consensus time, never a node's wall clock.
func NewTimedApp(chain string, db store.Store, check CheckFunc, execute TimedExecuteFunc, begin ...BeginBlockFunc) (*App, error) {
	if execute == nil || len(begin) > 1 {
		return nil, protocol.ErrRule
	}
	app, err := NewApp(chain, db, check, func(state.ReadView, []byte) (state.Transition, error) { return state.Transition{}, protocol.ErrRule })
	if err == nil {
		app.executeAt = execute
		if len(begin) == 1 {
			app.beginBlock = begin[0]
		}
	}
	return app, err
}

func NewApp(chain string, db store.Store, check CheckFunc, execute ExecuteFunc, maintenance ...MaintenanceFunc) (*App, error) {
	if chain == "" || db == nil || check == nil || execute == nil || len(maintenance) > 1 {
		return nil, protocol.ErrRule
	}
	a := &App{chain: chain, db: db, check: check, execute: execute, response: &abci.ResponseFinalizeBlock{}}
	if len(maintenance) == 1 {
		a.maintenance = maintenance[0]
	}
	e := db.View(func(v state.ReadView) error {
		raw, e := v.Get(metaKey)
		if errors.Is(e, state.ErrNotFound) {
			genesis, found, err := state.Load[protocol.Hash](v, state.Key(state.KeyGenesis))
			if err != nil {
				return err
			}
			if found {
				a.response.AppHash = bytes.Clone(genesis[:])
			}
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
	version, name := uint64(2), "utxo-v2"
	if a.executeAt != nil {
		version = 3
		name = "utxo-v3"
	}
	return &abci.ResponseInfo{Version: name, AppVersion: version, LastBlockHeight: a.committed.Height, LastBlockAppHash: bytes.Clone(a.response.AppHash)}, nil
}
func (a *App) InitChain(_ context.Context, r *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
	if r.ChainId != a.chain || r.InitialHeight > 1 {
		return nil, ErrBlock
	}
	return &abci.ResponseInitChain{AppHash: bytes.Clone(a.response.AppHash)}, nil
}
func (a *App) CheckTx(_ context.Context, r *abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
	if e := a.check(r.Tx); e != nil {
		return &abci.ResponseCheckTx{Code: 1, Log: e.Error()}, nil
	}
	return &abci.ResponseCheckTx{}, nil
}
func (a *App) PrepareProposal(_ context.Context, r *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
	requesttrace.Consensus.Mark("prepare_enter", "height", r.Height, "candidates", len(r.Txs))
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
		requesttrace.Settlement.Command(tx, "prepared", r.Height)
	}
	requesttrace.Consensus.Mark("prepare_done", "height", r.Height, "selected", len(out.Txs))
	return out, nil
}
func (a *App) ProcessProposal(_ context.Context, r *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	requesttrace.Consensus.Mark("process_enter", "height", r.Height, "transactions", len(r.Txs))
	for _, tx := range r.Txs {
		requesttrace.Settlement.Command(tx, "proposal_seen", r.Height)
		if a.check(tx) != nil {
			return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
		}
	}
	requesttrace.Consensus.Mark("process_done", "height", r.Height)
	return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_ACCEPT}, nil
}
func cloneResponse(r *abci.ResponseFinalizeBlock) *abci.ResponseFinalizeBlock {
	b, _ := r.Marshal()
	c := new(abci.ResponseFinalizeBlock)
	_ = c.Unmarshal(b)
	return c
}
func (a *App) FinalizeBlock(_ context.Context, r *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	requesttrace.Consensus.Mark("finalize_enter", "height", r.Height)
	a.mu.Lock()
	defer a.mu.Unlock()
	requesttrace.Consensus.Mark("finalize_locked", "height", r.Height)
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
	facts := make(map[string]protocol.FinalFact)
	var commitment finality.Commitment
	var leaves [][]byte
	e := a.db.View(func(v state.ReadView) error {
		overlay := state.NewOverlay(v)
		apply := func(transition state.Transition) error {
			for _, c := range transition.Changes {
				if len(c.Key) == 0 || c.Key[0] == 0xff {
					return errors.New("executor used reserved state key")
				}
			}
			candidate := state.NewOverlay(overlay)
			candidate.Apply(transition.Changes)
			for _, f := range transition.Facts {
				if f.Network != protocol.Digest("NETWORK", []byte(a.chain)) {
					return finality.ErrProof
				}
				raw, err := f.MarshalBinary()
				if err != nil {
					return err
				}
				key := f.SortKey()[:34]
				latest := state.Key(70, key)
				old, found, err := state.Load[protocol.FinalFact](candidate, latest)
				if err != nil {
					return err
				}
				if found && old.Revision >= f.Revision {
					if old.Revision == f.Revision && old.ID() == f.ID() {
						continue
					}
					return finality.ErrProof
				}
				if err = state.Put(candidate, latest, f); err != nil {
					return err
				}
				candidate.Set(state.Key(71, f.SortKey()), raw)
				facts[string(key)] = f
			}
			overlay.Apply(candidate.Changes())
			return nil
		}
		if a.beginBlock != nil {
			tr, err := a.beginBlock(v, BlockContext{Height: r.Height, Time: r.Time})
			if err != nil {
				return err
			}
			if err = apply(tr); err != nil {
				return err
			}
		}
		for i, tx := range r.Txs {
			response.TxResults[i] = &abci.ExecTxResult{}
			if err := a.check(tx); err != nil {
				response.TxResults[i].Code = 1
				response.TxResults[i].Log = "invalid command"
				continue
			}
			requesttrace.Settlement.Command(tx, "execute_start", r.Height)
			var transition state.Transition
			var err error
			if a.executeAt != nil {
				transition, err = a.executeAt(overlay, tx, BlockContext{Height: r.Height, Index: uint32(i), Time: r.Time})
			} else {
				transition, err = a.execute(overlay, tx)
			}
			requesttrace.Settlement.Command(tx, "execute_done", r.Height)
			if err != nil {
				response.TxResults[i].Code = 2
				response.TxResults[i].Log = "business rejected"
				continue
			}
			if err = apply(transition); err != nil {
				return err
			}
		}
		if a.maintenance != nil {
			transition, err := a.maintenance(overlay)
			if err != nil {
				return err
			}
			if err = apply(transition); err != nil {
				return err
			}
		}
		for _, c := range overlay.Changes() {
			old, err := v.Get(c.Key)
			if err != nil && !errors.Is(err, state.ErrNotFound) {
				return err
			}
			if c.Delete && errors.Is(err, state.ErrNotFound) {
				continue
			}
			if !c.Delete && err == nil && bytes.Equal(old, c.Value) {
				continue
			}
			changes = append(changes, c)
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	ordered := make([]protocol.FinalFact, 0, len(facts))
	for _, f := range facts {
		ordered = append(ordered, f)
	}
	sort.Slice(ordered, func(i, j int) bool { return bytes.Compare(ordered[i].SortKey(), ordered[j].SortKey()) < 0 })
	for _, f := range ordered {
		raw, e := f.MarshalBinary()
		if e != nil {
			return nil, e
		}
		leaves = append(leaves, raw)
	}
	if len(changes) > 0 {
		enc := new(protocol.Encoder)
		for _, c := range changes {
			enc.Bytes(c.Key)
			enc.Optional(c.Delete)
			enc.Bytes(c.Value)
		}
		commitment = finality.Commitment{Network: protocol.Digest("NETWORK", []byte(a.chain)), Height: r.Height, Previous: bytes.Clone(a.response.AppHash), WriteSet: protocol.Digest("WRITE_SET", enc.Data()), FactRoot: merkle.HashFromByteSlices(leaves)}
		root, e := commitment.Hash()
		if e != nil {
			return nil, e
		}
		response.AppHash = bytes.Clone(root[:])
	}

	raw, e := response.Marshal()
	if e != nil {
		return nil, e
	}
	a.pending = &commitRecord{Height: r.Height, BlockID: bytes.Clone(r.Hash), Response: raw, Commitment: commitment, Leaves: leaves}
	a.pendingChanges = changes
	requesttrace.Settlement.Block(r.Height, "finalize_done")
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
	requesttrace.Settlement.Block(a.pending.Height, "commit_start")
	raw, e := json.Marshal(a.pending)
	if e != nil {
		return nil, e
	}
	cs := append([]state.Change(nil), a.pendingChanges...)
	cs = append(cs, state.Change{Key: metaKey, Value: raw}, state.Change{Key: blockKey(a.pending.Height), Value: raw})
	for i, rawFact := range a.pending.Leaves {
		f, e := protocol.DecodeFact(rawFact)
		if e != nil {
			return nil, e
		}
		location, _ := json.Marshal(factLocation{Height: a.pending.Height, Index: i})
		cs = append(cs, state.Change{Key: proofKey(f.ID()), Value: location})
	}
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
	requesttrace.Settlement.Block(a.pending.Height, "commit_done")
	a.pending = nil
	a.pendingChanges = nil
	return &abci.ResponseCommit{}, nil
}
func (a *App) Query(_ context.Context, r *abci.RequestQuery) (*abci.ResponseQuery, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := &abci.ResponseQuery{Height: a.committed.Height}
	if len(r.Data) == 0 || r.Data[0] == 0xff {
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

type factLocation struct {
	Height int64
	Index  int
}

func blockKey(height int64) []byte {
	e := new(protocol.Encoder)
	e.U64(uint64(height))
	return append([]byte{0xff, 'b'}, e.Data()...)
}
func proofKey(id protocol.Hash) []byte { return append([]byte{0xff, 'p'}, id[:]...) }

// Proof returns committed material only. Clients still verify the pinned committee.
func (a *App) Proof(id protocol.Hash, header cmttypes.SignedHeader) (finality.FactProof, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var result finality.FactProof
	err := a.db.View(func(v state.ReadView) error {
		loc, found, e := state.Load[factLocation](v, proofKey(id))
		if e != nil {
			return e
		}
		if !found {
			return state.ErrNotFound
		}
		record, found, e := state.Load[commitRecord](v, blockKey(loc.Height))
		if e != nil {
			return e
		}
		if !found || loc.Index < 0 || loc.Index >= len(record.Leaves) {
			return finality.ErrProof
		}
		if header.Header == nil || header.Height != loc.Height+1 {
			return finality.ErrProof
		}
		root, e := record.Commitment.Hash()
		if e != nil || !bytes.Equal(root[:], header.AppHash) {
			return finality.ErrProof
		}
		_, paths := merkle.ProofsFromByteSlices(record.Leaves)
		f, e := protocol.DecodeFact(record.Leaves[loc.Index])
		if e != nil {
			return e
		}
		if f.ID() != id {
			return finality.ErrProof
		}
		result = finality.FactProof{Fact: f, Commitment: record.Commitment, Path: *paths[loc.Index], Header: header}
		return nil
	})
	return result, err
}
