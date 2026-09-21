package consensus

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cometbft/cometbft/abci/example/kvstore"
	cstypes "github.com/cometbft/cometbft/consensus/types"
	"github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/require"
)

type batchTestWAL struct {
	WAL
	messages   []WALMessage
	syncs      int
	failWrite  int
	failSync   bool
	beforeSync func()
	afterSync  func()
}

func (w *batchTestWAL) Write(m WALMessage) error {
	if w.failWrite > 0 && len(w.messages)+1 == w.failWrite {
		return errors.New("injected WAL write error")
	}
	if err := w.WAL.Write(m); err != nil {
		return err
	}
	w.messages = append(w.messages, m)
	return nil
}
func (w *batchTestWAL) FlushAndSync() error {
	w.syncs++
	if w.beforeSync != nil {
		w.beforeSync()
	}
	if w.failSync {
		return errors.New("injected WAL sync error")
	}
	if err := w.WAL.FlushAndSync(); err != nil {
		return err
	}
	if w.afterSync != nil {
		w.afterSync()
	}
	return nil
}
func (w *batchTestWAL) WriteSync(m WALMessage) error {
	if err := w.Write(m); err != nil {
		return err
	}
	return w.FlushAndSync()
}

// Incomplete data intentionally never reaches block decoding in these tests.
func batchTestState(t *testing.T, parts int) (*State, *batchTestWAL, []msgInfo) {
	t.Helper()
	cs, _ := randState(4)
	cs.SetLogger(log.NewNopLogger())
	cs.Step = cstypes.RoundStepPropose
	t.Cleanup(func() { _ = cs.eventBus.Stop() })
	w := &batchTestWAL{WAL: nilWAL{}}
	cs.wal = w
	ps := types.NewPartSetFromData(bytes.Repeat([]byte("a"), parts*int(types.BlockPartSizeBytes)), types.BlockPartSizeBytes)
	p := types.NewProposal(cs.Height, cs.Round, -1, types.BlockID{Hash: bytes.Repeat([]byte{1}, 32), PartSetHeader: ps.Header()})
	pb := p.ToProto()
	require.NoError(t, cs.privValidator.SignProposal(cs.state.ChainID, pb))
	p.Signature = pb.Signature
	messages := []msgInfo{{Msg: &ProposalMessage{p}}}
	for i := 0; i < parts; i++ {
		messages = append(messages, msgInfo{Msg: &BlockPartMessage{cs.Height, cs.Round, ps.GetPart(i)}})
	}
	return cs, w, messages
}

func TestProposalBatchAlreadyReady(t *testing.T) {
	cs, w, messages := batchTestState(t, 3)
	cs.internalMsgQueue <- messages[1]
	cs.internalMsgQueue <- messages[2] // leave one missing: no state transition
	w.beforeSync = func() { require.Nil(t, cs.Proposal, "no message may be processed before batch Sync") }
	cs.handleInternalMessage(messages[0])
	require.Len(t, w.messages, 3, "ready proposal and two parts should be persisted together")
	require.Equal(t, 1, w.syncs)
	require.EqualValues(t, 2, cs.ProposalBlockParts.Count())
	require.Empty(t, cs.internalMsgQueue)
}

func TestProposalBatchBoundaries(t *testing.T) {
	t.Run("completion leaves vote queued", func(t *testing.T) {
		cs, _, ms := batchTestState(t, 3)
		for _, m := range ms[1:] {
			cs.internalMsgQueue <- m
		}
		cs.internalMsgQueue <- msgInfo{Msg: &VoteMessage{}}
		got, next := cs.collectProposalBatch(ms[0])
		require.Len(t, got, 4)
		require.Nil(t, next)
		require.Len(t, cs.internalMsgQueue, 1)
	})
	t.Run("peer parts reduce remaining budget", func(t *testing.T) {
		cs, _, ms := batchTestState(t, 3)
		cs.handleMsg(ms[0])
		cs.handleMsg(ms[1])
		cs.handleMsg(ms[2])
		cs.internalMsgQueue <- ms[1] // duplicate must not follow a completing part in this batch
		got, next := cs.collectProposalBatch(ms[3])
		require.Len(t, got, 1)
		require.Nil(t, next)
		require.Len(t, cs.internalMsgQueue, 1)
	})
	t.Run("duplicate counts conservatively", func(t *testing.T) {
		cs, _, ms := batchTestState(t, 3)
		cs.handleMsg(ms[0])
		cs.handleMsg(ms[1])
		cs.internalMsgQueue <- ms[2]
		cs.internalMsgQueue <- ms[3]
		got, next := cs.collectProposalBatch(ms[1])
		require.Len(t, got, 2)
		require.Nil(t, next)
		require.Len(t, cs.internalMsgQueue, 1)
	})
	t.Run("vote boundary is retained", func(t *testing.T) {
		cs, _, ms := batchTestState(t, 3)
		vote := msgInfo{Msg: &VoteMessage{}}
		cs.internalMsgQueue <- ms[1]
		cs.internalMsgQueue <- vote
		cs.internalMsgQueue <- ms[2]
		got, next := cs.collectProposalBatch(ms[0])
		require.Len(t, got, 2)
		require.Equal(t, vote, *next)
		require.Len(t, cs.internalMsgQueue, 1)
	})
	t.Run("other round is not batched", func(t *testing.T) {
		cs, _, ms := batchTestState(t, 3)
		wrong := *ms[1].Msg.(*BlockPartMessage)
		wrong.Round++
		cs.internalMsgQueue <- msgInfo{Msg: &wrong}
		got, next := cs.collectProposalBatch(ms[0])
		require.Len(t, got, 1)
		require.Equal(t, &wrong, next.Msg)
	})
	t.Run("late phase uses original path", func(t *testing.T) {
		cs, _, ms := batchTestState(t, 3)
		cs.Step = cstypes.RoundStepPrecommit
		cs.internalMsgQueue <- ms[1]
		got, next := cs.collectProposalBatch(ms[0])
		require.Len(t, got, 1)
		require.Nil(t, next)
		require.Len(t, cs.internalMsgQueue, 1)
	})
	t.Run("byte bound", func(t *testing.T) {
		cs, _, ms := batchTestState(t, 10)
		for _, m := range ms[1:] {
			cs.internalMsgQueue <- m
		}
		got, next := cs.collectProposalBatch(ms[0])
		require.Len(t, got, 9)
		require.Nil(t, next) // proposal + eight 64 KiB parts
		require.Len(t, cs.internalMsgQueue, 2)
	})
	t.Run("message bound", func(t *testing.T) {
		cs, _, ms := batchTestState(t, 20)
		for i := 0; i < 20; i++ {
			cs.internalMsgQueue <- msgInfo{Msg: &BlockPartMessage{Height: cs.Height, Round: cs.Round, Part: &types.Part{}}}
		}
		got, next := cs.collectProposalBatch(ms[0])
		require.Len(t, got, 16)
		require.Nil(t, next)
		require.Len(t, cs.internalMsgQueue, 5)
	})
}

func TestProposalBatchStorageFailureDoesNotApply(t *testing.T) {
	for _, failAt := range []int{1, 2, 3, 4} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			cs, w, ms := batchTestState(t, 3)
			cs.internalMsgQueue <- ms[1]
			cs.internalMsgQueue <- ms[2]
			if failAt == 4 {
				w.failSync = true
			} else {
				w.failWrite = failAt
			}
			require.Panics(t, func() { cs.handleInternalMessage(ms[0]) })
			require.Nil(t, cs.Proposal)
			require.Nil(t, cs.ProposalBlockParts)
		})
	}
}

// Actual protobuf block, signatures, file WAL and replay; no batch-only WAL format.
func TestProposalBatchDurableReplay(t *testing.T) {
	for _, crash := range []string{"none", "write_prefix", "sync_error", "after_sync", "after_proposal"} {
		t.Run(crash, func(t *testing.T) {
			cs, _ := randState(4)
			dir := t.TempDir()
			pv := privval.NewFilePV(cs.privValidator.(types.MockPV).PrivKey, filepath.Join(dir, "key.json"), filepath.Join(dir, "state.json"))
			pv.Save()
			cs.SetPrivValidator(pv)
			cs.SetLogger(log.NewNopLogger())
			cs.Step = cstypes.RoundStepPropose
			t.Cleanup(func() { _ = cs.eventBus.Stop() })
			block, err := cs.createProposalBlock(context.Background())
			require.NoError(t, err)
			pb, err := block.ToProto()
			require.NoError(t, err)
			tx := append([]byte("key="), bytes.Repeat([]byte("v"), 128*1024)...)
			pb.Data.Txs = [][]byte{tx}
			pb.Header.DataHash = types.Txs{tx}.Hash()
			block, err = types.BlockFromProto(pb)
			require.NoError(t, err)
			ps, err := block.MakePartSet(types.BlockPartSizeBytes)
			require.NoError(t, err)
			require.EqualValues(t, 3, ps.Total())
			p := types.NewProposal(cs.Height, cs.Round, -1, types.BlockID{Hash: block.Hash(), PartSetHeader: ps.Header()})
			pp := p.ToProto()
			require.NoError(t, cs.privValidator.SignProposal(cs.state.ChainID, pp))
			p.Signature = pp.Signature
			first := msgInfo{Msg: &ProposalMessage{p}}
			for i := 0; i < int(ps.Total()); i++ {
				cs.internalMsgQueue <- msgInfo{Msg: &BlockPartMessage{cs.Height, cs.Round, ps.GetPart(i)}}
			}
			path := filepath.Join(t.TempDir(), "wal")
			disk, err := NewWAL(path)
			require.NoError(t, err)
			t.Cleanup(func() { _ = disk.Group().Head.Close() })
			w := &batchTestWAL{WAL: disk}
			cs.wal = w
			if crash == "write_prefix" {
				w.failWrite = 3
			}
			if crash == "sync_error" {
				w.failSync = true
			}
			if crash == "after_sync" {
				w.afterSync = func() { panic("crash after batch Sync") }
			}
			if crash == "after_proposal" {
				original := cs.setProposal
				cs.setProposal = func(p *types.Proposal) error {
					err := original(p)
					if err == nil {
						panic("crash after handling proposal")
					}
					return err
				}
			}
			if crash == "none" {
				cs.handleInternalMessage(first)
				require.Equal(t, cstypes.RoundStepPrevote, cs.Step)
				// A duplicate part must be logged after the transition, never prefetched into this batch.
				cs.handleInternalMessage(msgInfo{Msg: &BlockPartMessage{cs.Height, cs.Round, ps.GetPart(0)}})
				require.IsType(t, types.EventDataRoundState{}, w.messages[4])
				require.IsType(t, msgInfo{}, w.messages[5])
				require.NoError(t, disk.FlushAndSync())
			} else {
				require.Panics(t, func() { cs.handleInternalMessage(first) })
			}
			if crash == "write_prefix" || crash == "sync_error" {
				require.Nil(t, cs.Proposal)
				// Even an errored batch can leave a valid durable prefix. Exercise that
				// allowed outcome; do not assume errors imply atomic rollback.
				require.NoError(t, disk.FlushAndSync())
			}
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			decoder := NewWALDecoder(bytes.NewReader(raw))
			reloaded := privval.LoadFilePV(filepath.Join(dir, "key.json"), filepath.Join(dir, "state.json"))
			replay := newState(cs.state.Copy(), reloaded, kvstore.NewInMemoryApplication())
			replay.SetLogger(log.NewNopLogger())
			replay.Step = cstypes.RoundStepPropose
			replay.replayMode = true
			t.Cleanup(func() { _ = replay.eventBus.Stop() })
			n := 0
			for {
				msg, err := decoder.Decode()
				if errors.Is(err, io.EOF) {
					break
				}
				require.NoError(t, err)
				require.NoError(t, replay.readReplayMessage(msg, nil))
				n++
			}
			if crash == "write_prefix" {
				require.Equal(t, 2, n)
				require.Equal(t, cstypes.RoundStepPropose, replay.Step)
				// The rest may arrive from peers after replay. Duplicate parts are harmless.
				for i := 0; i < int(ps.Total()); i++ {
					replay.handleMsg(msgInfo{Msg: &BlockPartMessage{replay.Height, replay.Round, ps.GetPart(i)}})
				}
			} else {
				require.GreaterOrEqual(t, n, 4)
			}
			require.Equal(t, cstypes.RoundStepPrevote, replay.Step)
			require.Equal(t, block.Hash(), replay.ProposalBlock.Hash())
			require.EqualValues(t, 2, reloaded.LastSignState.Step) // persisted prevote
			if crash == "none" {
				require.Equal(t, pv.LastSignState.Signature, reloaded.LastSignState.Signature, "replay must reuse the prior vote signature")
			}
		})
	}
}
