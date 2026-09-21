package committee

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	abcicli "github.com/cometbft/cometbft/abci/client"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/proxy"
	cmtstore "github.com/cometbft/cometbft/store"
	"utxo/crypto/chameleon"
	"utxo/internal/requesttrace"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type connsyncCommitGate struct {
	store.Store
	entered, release chan struct{}
}

func (s *connsyncCommitGate) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	close(s.entered)
	<-s.release
	return s.Store.Update(fn)
}

func TestConnSyncDirectChecksDuringFinalizeAndCommit(t *testing.T) {
	// Configure diagnostics before any goroutine, as the real node does.
	oldConsensus, oldSettlement := requesttrace.Consensus, requesttrace.Settlement
	requesttrace.Consensus, requesttrace.Settlement = &requesttrace.ConsensusRecorder{}, requesttrace.NewSettlementRecorder(2048)
	defer func() { requesttrace.Consensus, requesttrace.Settlement = oldConsensus, oldSettlement }()
	cfg, fixture, policy, txs := cacheFixture(t, 34)
	cold := txs[32:]
	txs = txs[:32]
	repair := protocol.RepairInput{Network: cfg.Network, Output: protocol.OutputID{1}, Height: 1, TransactionBytes: []byte{1}, Parts: []chameleon.Opening{{}}}
	repairRaw, err := repair.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	repair.Network = protocol.Hash{9}
	wrongRepair, err := repair.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	clock := protocol.ClockTick(cfg.Network, 1)
	wrongClock := protocol.ClockTick(protocol.Hash{9}, 1)
	payment, err := protocol.DecodeDirectSubmission(txs[0])
	if err != nil {
		t.Fatal(err)
	}
	invalid := bytes.Clone(txs[0])
	position := bytes.Index(invalid, payment.Tx.Auth[0].Signature[:])
	if position < 0 {
		t.Fatal("owner signature absent")
	}
	invalid[position] ^= 1
	conflict, err := fixture.FastTransaction(0, 999, policy)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := fixture.DirectCertificate(conflict, policy)
	if err != nil {
		t.Fatal(err)
	}
	conflictRaw, err := (protocol.DirectPayment{Tx: conflict, Certificate: certificate}).Submission().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	blockTxs := append(append([][]byte(nil), txs...), txs[0], clock, repairRaw, conflictRaw)
	var reference []state.Entry
	var referenceResponse *abci.ResponseFinalizeBlock
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprint("parallel=", parallel), func(t *testing.T) {
			base := store.NewMemory()
			defer base.Close()
			engine, err := NewEngine(cfg, base)
			if err != nil {
				t.Fatal(err)
			}
			if err := engine.Check(repairRaw); err == nil {
				t.Fatal("repair enabled without startup configuration")
			}
			blockDB := dbm.NewMemDB()
			defer blockDB.Close()
			if err := engine.EnableRepair(cmtstore.NewBlockStore(blockDB)); err != nil {
				t.Fatal(err)
			}
			var db store.Store = base
			execute := engine.ExecuteAt
			finalEntered, finalRelease := make(chan struct{}), make(chan struct{})
			commitEntered, commitRelease := make(chan struct{}), make(chan struct{})
			if parallel {
				var entered sync.Once
				execute = func(v state.ReadView, raw []byte, b BlockContext) (state.Transition, error) {
					entered.Do(func() { close(finalEntered); <-finalRelease })
					return engine.ExecuteAt(v, raw, b)
				}
				db = &connsyncCommitGate{Store: base, entered: commitEntered, release: commitRelease}
			}
			app, err := NewTimedApp("verify-cache", db, engine.Check, execute, engine.BeginBlock)
			if err != nil {
				t.Fatal(err)
			}
			creator := proxy.NewLocalClientCreator(app)
			if parallel {
				creator = proxy.NewConnSyncLocalClientCreator(app)
			}
			consensus, err := creator.NewABCIClient()
			if err != nil {
				t.Fatal(err)
			}
			mempool, err := creator.NewABCIClient()
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			check := func(client abcicli.Client, raw []byte, valid bool) error {
				result, err := client.CheckTx(ctx, &abci.RequestCheckTx{Tx: raw})
				if err != nil {
					return err
				}
				if (result.Code == 0) != valid {
					return fmt.Errorf("CheckTx code=%d valid=%v", result.Code, valid)
				}
				return nil
			}
			checkBranches := func() error {
				for _, branch := range []struct {
					raw   []byte
					valid bool
				}{
					{invalid, false}, {clock, true}, {wrongClock, false},
					{repairRaw, true}, {wrongRepair, false}, {repairRaw[:8], false},
				} {
					if err := check(mempool, branch.raw, branch.valid); err != nil {
						return err
					}
				}
				return nil
			}
			// Repeated cold/warm checks overlap the consensus connection; these
			// checks never acquire the ledger writer or change the transaction.
			stop, checked := make(chan struct{}), make(chan error, 1)
			if parallel {
				go func() {
					for {
						for _, raw := range txs {
							select {
							case <-stop:
								checked <- nil
								return
							default:
							}
							if err := check(mempool, raw, true); err != nil {
								checked <- err
								return
							}
							if err := checkBranches(); err != nil {
								checked <- err
								return
							}
						}
					}
				}()
			}
			queriesDone := make(chan error, 1)
			if parallel {
				query, err := creator.NewABCIClient()
				if err != nil {
					t.Fatal(err)
				}
				go func() {
					for {
						select {
						case <-stop:
							queriesDone <- nil
							return
						default:
						}
						info, err := query.Info(ctx, &abci.RequestInfo{})
						if err != nil {
							queriesDone <- err
							return
						}
						if info.LastBlockHeight < 0 || info.LastBlockHeight > 1 {
							queriesDone <- fmt.Errorf("invalid committed height")
							return
						}
						result, err := query.Query(ctx, &abci.RequestQuery{Data: rules.DirectCreationKey(payment.Summary().OutputID(0), 0)})
						if err != nil {
							queriesDone <- err
							return
						}
						if (result.Height == 0 && result.Code != 1) || (result.Height == 1 && result.Code != 0) {
							queriesDone <- fmt.Errorf("query exposed pending state at height %d", result.Height)
							return
						}
						requesttrace.Consensus.Snapshot()
						requesttrace.Settlement.ForSpend(protocol.Hash(payment.Authorization.Fact).String())
					}
				}()
			}
			defer func() {
				if parallel {
					close(stop)
					if err := <-checked; err != nil {
						t.Error(err)
					}
					if err := <-queriesDone; err != nil {
						t.Error(err)
					}
				}
			}()
			prepared, err := consensus.PrepareProposal(ctx, &abci.RequestPrepareProposal{Height: 1, Txs: blockTxs, MaxTxBytes: 1 << 30})
			if err != nil || len(prepared.Txs) != len(blockTxs) {
				t.Fatal("prepare", err)
			}
			processed, err := consensus.ProcessProposal(ctx, &abci.RequestProcessProposal{Height: 1, Txs: prepared.Txs})
			if err != nil || processed.Status != abci.ResponseProcessProposal_ACCEPT {
				t.Fatal("process", err)
			}
			var finalized *abci.ResponseFinalizeBlock
			probeIndex := 0
			run := func(fn func() error, entered, release chan struct{}) {
				if !parallel {
					if err := fn(); err != nil {
						t.Fatal(err)
					}
					return
				}
				finished := make(chan error, 1)
				go func() { finished <- fn() }()
				select {
				case <-entered:
				case err := <-finished:
					t.Fatal("consensus returned before reaching gate", err)
				}
				probe := make(chan error, 1)
				go func() {
					if err := check(mempool, cold[probeIndex], true); err != nil {
						probe <- err
						return
					}
					probe <- checkBranches()
				}()
				var probeError error
				select {
				case probeError = <-probe:
				case <-time.After(5 * time.Second):
					probeError = fmt.Errorf("CheckTx blocked by the consensus connection")
				}
				close(release)
				if err := <-finished; err != nil {
					t.Error(err)
				}
				if probeError != nil {
					t.Fatal(probeError)
				}
				probeIndex++
			}
			run(func() error {
				var err error
				finalized, err = consensus.FinalizeBlock(ctx, &abci.RequestFinalizeBlock{Height: 1, Hash: bytes.Repeat([]byte{1}, 32), Time: time.Unix(100, 0), Txs: prepared.Txs})
				return err
			}, finalEntered, finalRelease)
			run(func() error { _, err := consensus.Commit(ctx, &abci.RequestCommit{}); return err }, commitEntered, commitRelease)
			for i, result := range finalized.TxResults {
				want := uint32(0)
				// Syntactic repair admission does not authorize repair execution.
				if i >= len(blockTxs)-2 {
					want = 2
				}
				if result.Code != want {
					t.Fatalf("transaction %d: code=%d want=%d", i, result.Code, want)
				}
			}
			if err := check(mempool, invalid, false); err != nil {
				t.Fatal(err)
			}
			entries := cacheLedger(t, base)
			if !parallel {
				reference, referenceResponse = entries, finalized
			} else if !reflect.DeepEqual(entries, reference) || !reflect.DeepEqual(finalized, referenceResponse) {
				t.Fatal("connection concurrency changed the committed ledger or execution response")
			}
		})
	}
}
