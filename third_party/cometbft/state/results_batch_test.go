package state

import (
	"bytes"
	"errors"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

var errResultsWrite = errors.New("injected results write failure")

type resultsFaultDB struct {
	dbm.DB
	fail   string
	closed bool
}

func (d *resultsFaultDB) SetSync(k, v []byte) error {
	if d.fail == "sync" {
		return errResultsWrite
	}
	return d.DB.SetSync(k, v)
}

func (d *resultsFaultDB) NewBatch() dbm.Batch {
	return &resultsFaultBatch{Batch: d.DB.NewBatch(), owner: d}
}

type resultsFaultBatch struct {
	dbm.Batch
	owner *resultsFaultDB
	sets  int
}

func (b *resultsFaultBatch) Set(k, v []byte) error {
	b.sets++
	if b.owner.fail == "second set" && b.sets == 2 {
		return errResultsWrite
	}
	return b.Batch.Set(k, v)
}

func (b *resultsFaultBatch) WriteSync() error {
	if b.owner.fail == "sync" {
		return errResultsWrite
	}
	err := b.Batch.WriteSync()
	if err == nil && b.owner.fail == "after sync" {
		return errResultsWrite
	}
	return err
}

func (b *resultsFaultBatch) Close() error {
	b.owner.closed = true
	return b.Batch.Close()
}

// These injected failures occur before the batch write reaches its database.
func TestFinalizeResultsFailureBeforeWriteDoesNotPublishPartialHistory(t *testing.T) {
	for _, fail := range []string{"sync", "second set"} {
		t.Run(fail, func(t *testing.T) {
			database := &resultsFaultDB{DB: dbm.NewMemDB()}
			defer database.Close()
			s := NewStore(database, StoreOptions{})
			old := &abci.ResponseFinalizeBlock{AppHash: []byte{1}}
			require.NoError(t, s.SaveFinalizeBlockResponse(1, old))
			database.fail, database.closed = fail, false
			require.ErrorIs(t, s.SaveFinalizeBlockResponse(2, &abci.ResponseFinalizeBlock{AppHash: []byte{2}}), errResultsWrite)
			missing, err := database.Get(calcABCIResponsesKey(2))
			require.NoError(t, err)
			require.Empty(t, missing, "failed save must not publish just the historical half")
			last, err := s.LoadLastFinalizeBlockResponse(1)
			require.NoError(t, err)
			require.Equal(t, old, last)
			require.True(t, database.closed, "release batch on failure")
		})
	}
}

func TestFinalizeResultsUncertainSyncReturnsErrorWithoutRollback(t *testing.T) {
	database := &resultsFaultDB{DB: dbm.NewMemDB(), fail: "after sync"}
	defer database.Close()
	s := NewStore(database, StoreOptions{})
	response := &abci.ResponseFinalizeBlock{AppHash: []byte{3}}
	require.ErrorIs(t, s.SaveFinalizeBlockResponse(3, response), errResultsWrite)
	// A write may have become durable despite an error being returned to its caller.
	// The caller must halt/recover, not assume failure implies no data was stored.
	last, err := s.LoadLastFinalizeBlockResponse(3)
	require.NoError(t, err)
	require.Equal(t, response, last)
	history, err := s.LoadFinalizeBlockResponse(3)
	require.NoError(t, err)
	require.Equal(t, response, history)
	require.True(t, database.closed)
}

func TestFinalizeResultsLargeBatchAndExistingHistory(t *testing.T) {
	dir := t.TempDir()
	options := &opt.Options{WriteBuffer: 64 << 10}
	database, err := dbm.NewGoLevelDBWithOpts("results", dir, options)
	require.NoError(t, err)
	s := NewStore(database, StoreOptions{})
	small := &abci.ResponseFinalizeBlock{AppHash: []byte{1}}
	require.NoError(t, s.SaveFinalizeBlockResponse(1, small))
	large := &abci.ResponseFinalizeBlock{AppHash: []byte{2}, TxResults: []*abci.ExecTxResult{{Data: bytes.Repeat([]byte{9}, 128<<10)}}}
	require.NoError(t, s.SaveFinalizeBlockResponse(2, large))
	require.NoError(t, database.Close())
	database, err = dbm.NewGoLevelDBWithOpts("results", dir, options)
	require.NoError(t, err)
	defer database.Close()
	s = NewStore(database, StoreOptions{})
	last, err := s.LoadLastFinalizeBlockResponse(2)
	require.NoError(t, err)
	require.Equal(t, large, last)
	for h, want := range map[int64]*abci.ResponseFinalizeBlock{1: small, 2: large} {
		got, err := s.LoadFinalizeBlockResponse(h)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	discard := NewStore(database, StoreOptions{DiscardABCIResponses: true})
	require.NoError(t, discard.SaveFinalizeBlockResponse(3, small))
	old, err := s.LoadFinalizeBlockResponse(2)
	require.NoError(t, err)
	require.Equal(t, large, old, "discard mode must not delete existing history")
}

func TestFinalizeResultsSurviveReopen(t *testing.T) {
	for _, discard := range []bool{false, true} {
		t.Run(map[bool]string{false: "history and recovery", true: "recovery only"}[discard], func(t *testing.T) {
			dir := t.TempDir()
			database, err := dbm.NewGoLevelDB("results", dir)
			require.NoError(t, err)
			s := NewStore(database, StoreOptions{DiscardABCIResponses: discard})
			response := &abci.ResponseFinalizeBlock{AppHash: []byte{1, 2}, TxResults: []*abci.ExecTxResult{nil, {Code: 0, Data: []byte("ok")}}}
			require.NoError(t, s.SaveFinalizeBlockResponse(7, response))
			require.Len(t, response.TxResults, 1)
			require.NoError(t, database.Close())
			database, err = dbm.NewGoLevelDB("results", dir)
			require.NoError(t, err)
			defer database.Close()
			s = NewStore(database, StoreOptions{DiscardABCIResponses: discard})
			last, err := s.LoadLastFinalizeBlockResponse(7)
			require.NoError(t, err)
			require.Equal(t, response, last)
			_, err = s.LoadLastFinalizeBlockResponse(8)
			require.Error(t, err)
			if discard {
				raw, err := database.Get(calcABCIResponsesKey(7))
				require.NoError(t, err)
				require.Empty(t, raw)
			} else {
				history, err := s.LoadFinalizeBlockResponse(7)
				require.NoError(t, err)
				require.Equal(t, response, history)
			}
		})
	}
}
