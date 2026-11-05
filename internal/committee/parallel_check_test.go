package committee

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/mempool"
	"github.com/cometbft/cometbft/proxy"
	cmttypes "github.com/cometbft/cometbft/types"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestParallelCheckBoundCallbacksFlushAndLedger(t *testing.T) {
	cfg, fixture, policy, txs := cacheFixture(t, 8)
	parsed, err := protocol.DecodeDirectSubmission(txs[0])
	if err != nil {
		t.Fatal(err)
	}
	invalid := bytes.Clone(txs[0])
	pos := bytes.Index(invalid, parsed.Tx.Auth[0].Signature[:])
	if pos < 0 {
		t.Fatal("owner signature absent")
	}
	invalid[pos] ^= 1
	conflict, err := fixture.FastTransaction(0, 999, policy)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := fixture.DirectCertificate(conflict, policy)
	if err != nil {
		t.Fatal(err)
	}
	conflictRaw, err := (protocol.DirectPayment{Tx: conflict, Certificate: cert}).Submission().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	blockTxs := append(append([][]byte(nil), txs...), txs[0], conflictRaw)
	var reference []state.Entry
	var referenceResponse *abci.ResponseFinalizeBlock
	for _, mode := range []string{"serial", "parallel-cache", "parallel-no-cache"} {
		t.Run(mode, func(t *testing.T) {
			db := store.NewMemory()
			defer db.Close()
			engine, err := NewEngine(cfg, db)
			if err != nil {
				t.Fatal(err)
			}
			engine.directCache.disabled = mode == "parallel-no-cache"
			app, err := NewTimedApp("verify-cache", db, engine.Check, engine.ExecuteAt, engine.BeginBlock)
			if err != nil {
				t.Fatal(err)
			}
			creator := proxy.NewConnSyncLocalClientCreator(app)
			var pool *mempool.CListMempool
			if mode != "serial" {
				parallel := NewParallelCheckClientCreator(app, engine).(*parallelCheckCreator)
				creator = parallel
				var active, peak, warms, callbacks atomic.Int32
				entered, release := make(chan struct{}, len(txs)), make(chan struct{})
				var releaseOnce sync.Once
				var workers sync.WaitGroup
				defer func() { releaseOnce.Do(func() { close(release) }); workers.Wait() }()
				check := parallel.check
				parallel.check = func(raw []byte) error {
					n := active.Add(1)
					defer active.Add(-1)
					for previous := peak.Load(); n > previous; previous = peak.Load() {
						if peak.CompareAndSwap(previous, n) {
							break
						}
					}
					warms.Add(1)
					select {
					case entered <- struct{}{}:
					default:
					}
					<-release
					return check(raw)
				}
				client, err := creator.NewABCIClient()
				if err != nil {
					t.Fatal(err)
				}
				mc := config.DefaultMempoolConfig()
				mc.Recheck = true
				pool = mempool.NewCListMempool(mc, proxy.NewAppConnMempool(client, proxy.NopMetrics()), 0)
				errors := make(chan error, len(txs))
				for _, raw := range txs {
					workers.Add(1)
					go func(raw []byte) {
						defer workers.Done()
						errors <- pool.CheckTx(cmttypes.Tx(raw), func(r *abci.ResponseCheckTx) {
							callbacks.Add(1)
							if r.Code != 0 {
								t.Errorf("valid new transaction rejected: %d", r.Code)
							}
						}, mempool.TxInfo{})
					}(raw)
				}
				for i := 0; i < 4; i++ {
					select {
					case <-entered:
					case <-time.After(5 * time.Second):
						t.Fatal("four prechecks did not overlap")
					}
				}
				if active.Load() != 4 {
					t.Fatalf("active prechecks=%d want=4", active.Load())
				}
				// Real mempool writer/Flush/recheck cannot pass the prechecks, which
				// remain inside CheckTx's updateMtx read-side critical section.
				locked, updated := make(chan struct{}), make(chan error, 1)
				go func() {
					pool.Lock()
					defer pool.Unlock()
					close(locked)
					if active.Load() != 0 || pool.Size() < 4 {
						updated <- fmt.Errorf("writer overtook in-flight admissions")
						return
					}
					before := warms.Load()
					if err := pool.FlushAppConn(); err != nil {
						updated <- err
						return
					}
					if err := pool.Update(1, nil, nil, nil, nil); err != nil {
						updated <- err
						return
					}
					if warms.Load() != before {
						updated <- fmt.Errorf("recheck invoked parallel warming")
						return
					}
					updated <- nil
				}()
				select {
				case <-locked:
					t.Fatal("writer bypassed blocked CheckTx")
				default:
				}
				releaseOnce.Do(func() { close(release) })
				workers.Wait()
				for range txs {
					if err := <-errors; err != nil {
						t.Fatal(err)
					}
				}
				if err := <-updated; err != nil {
					t.Fatal(err)
				}
				if peak.Load() != 4 || warms.Load() != int32(len(txs)) || callbacks.Load() != int32(len(txs)) || pool.Size() != len(txs) {
					t.Fatalf("peak=%d warms=%d callbacks=%d size=%d", peak.Load(), warms.Load(), callbacks.Load(), pool.Size())
				}
				for _, c := range []struct {
					raw   []byte
					valid bool
				}{{invalid, false}, {conflictRaw, true}} {
					calls := 0
					err := pool.CheckTx(cmttypes.Tx(c.raw), func(r *abci.ResponseCheckTx) {
						calls++
						if (r.Code == 0) != c.valid {
							t.Errorf("callback code=%d valid=%v", r.Code, c.valid)
						}
					}, mempool.TxInfo{})
					if err != nil || calls != 1 {
						t.Fatalf("CheckTx error=%v callbacks=%d", err, calls)
					}
				}
				if pool.Size() != len(txs)+1 {
					t.Fatal("invalid signature reached mempool")
				}
			}
			consensus, err := creator.NewABCIClient()
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			prepared, err := consensus.PrepareProposal(ctx, &abci.RequestPrepareProposal{Height: 1, Txs: blockTxs, MaxTxBytes: 1 << 30})
			if err != nil || len(prepared.Txs) != len(blockTxs) {
				t.Fatal("prepare", err)
			}
			processed, err := consensus.ProcessProposal(ctx, &abci.RequestProcessProposal{Height: 1, Txs: prepared.Txs})
			if err != nil || processed.Status != abci.ResponseProcessProposal_ACCEPT {
				t.Fatal("process", err)
			}
			result, err := consensus.FinalizeBlock(ctx, &abci.RequestFinalizeBlock{Height: 1, Hash: bytes.Repeat([]byte{1}, 32), Time: time.Unix(100, 0), Txs: prepared.Txs})
			if err != nil {
				t.Fatal(err)
			}
			for i, r := range result.TxResults {
				want := uint32(0)
				if i == len(blockTxs)-1 {
					want = 2
				}
				if r.Code != want {
					t.Fatalf("transaction %d code=%d want=%d", i, r.Code, want)
				}
			}
			if pool != nil {
				pool.Lock()
			}
			_, err = consensus.Commit(ctx, &abci.RequestCommit{})
			if pool != nil {
				committed := make(cmttypes.Txs, len(blockTxs))
				for i, raw := range blockTxs {
					committed[i] = cmttypes.Tx(raw)
				}
				updateErr := pool.Update(1, committed, result.TxResults, nil, nil)
				pool.Unlock()
				if updateErr != nil {
					t.Fatal(updateErr)
				}
				if pool.Size() != 0 {
					t.Fatal("committed mempool did not drain")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			ledger := cacheLedger(t, db)
			if mode == "serial" {
				reference, referenceResponse = ledger, result
			} else if !reflect.DeepEqual(reference, ledger) || !reflect.DeepEqual(referenceResponse, result) {
				t.Fatal("parallel warming changed final accounting")
			}
		})
	}
}
