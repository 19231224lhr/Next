//go:build comet_v3

package main

import (
	"context"
	"encoding/json"
	"fmt"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/rpc/client/local"
	"github.com/cometbft/cometbft/types"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/crypto/chameleon"
	"utxo/internal/committee"
	"utxo/internal/redaction"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func startRepairRuntime(parent context.Context, c configuration, n cfg.Network, db store.Store, app *committee.App, node *node.Node, mux *http.ServeMux) (func(), error) {
	if n.Direct == nil {
		return func() {}, nil
	}
	policy, err := n.Direct.Policy(n.Schedule, n.Organizations)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(c.RepairKeyFile)
	if err != nil {
		return nil, err
	}
	signer, err := chameleon.DecodeSigner(policy.Key, raw)
	if err != nil {
		return nil, err
	}
	if signer.Index() != uint(c.Index)+1 {
		return nil, protocol.ErrAuth
	}
	localDB, err := store.Open(filepath.Join(c.DataDir, "repair-worker.db"), store.Identity{Network: n.ChainID, Role: "repair-worker", Node: fmt.Sprint(c.Index), Schema: 3})
	if err != nil {
		return nil, err
	}
	blocks := node.BlockStore()
	client := local.New(node)
	trace := func(stage string, id protocol.Hash, started time.Time, err error, extra ...any) {
		if os.Getenv("UTXO_SETTLEMENT_TRACE") != "1" {
			return
		}
		fields := []any{"node", c.Index, "repair", id.String(), "stage", stage, "unix_ns", time.Now().UnixNano(), "duration_ns", time.Since(started).Nanoseconds()}
		if err != nil {
			fields = append(fields, "error", err.Error())
		}
		slog.Info("repair_trace", append(fields, extra...)...)
	}
	head := func() (int64, int64) {
		info, err := app.Info(context.Background(), &abci.RequestInfo{})
		if err != nil || info.LastBlockHeight == 0 {
			return 0, 0
		}
		meta := blocks.LoadBlockMeta(info.LastBlockHeight)
		if meta == nil {
			return 0, 0
		}
		return info.LastBlockHeight, meta.Header.Time.Unix()
	}
	// Fault injection affects adaptation only; the decision worker stays active.
	pauseFile := os.Getenv("UTXO_EXPERIMENT_ADAPTATION_PAUSE_FILE")
	slots := make(chan struct{}, 2)
	handler := func(mode int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if pauseFile != "" {
				if _, err := os.Stat(pauseFile); err == nil {
					http.Error(w, "ADAPTATION_PAUSED", http.StatusServiceUnavailable)
					return
				}
			}
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			default:
				http.Error(w, "BUSY", 429)
				return
			}
			_, now := head()
			var command protocol.RepairInput
			var batch protocol.RepairBatch
			var request any = &command
			if mode == 2 {
				request = &batch
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, protocol.MaxRequestBytes))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(request); err != nil {
				http.Error(w, "INVALID_REQUEST", 400)
				return
			}
			var response [][]byte
			err := db.View(func(v state.ReadView) error {
				var shares []chameleon.Contribution
				if mode == 2 {
					var err error
					shares, err = redaction.BatchPartShares(v, blocks, policy, signer, batch, now)
					if err != nil {
						return err
					}
				} else if mode == 1 {
					var err error
					shares, err = redaction.PartShares(v, blocks, policy, signer, command, now)
					if err != nil {
						return err
					}
				} else {
					s, err := redaction.InputShare(v, blocks, policy, signer, command.Output, now)
					if err != nil {
						return err
					}
					shares = []chameleon.Contribution{s}
				}
				for _, s := range shares {
					raw, err := s.MarshalBinary()
					if err != nil {
						return err
					}
					response = append(response, raw)
				}
				return nil
			})
			if err != nil {
				http.Error(w, "REPAIR_NOT_ELIGIBLE", 409)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		}
	}
	mux.HandleFunc("POST /v3/repair/input-share", handler(0))
	mux.HandleFunc("POST /v3/repair/part-shares", handler(1))
	mux.HandleFunc("POST /v3/repair/batch-part-shares", handler(2))
	mux.HandleFunc("GET /v3/repairs/{output}/status", func(w http.ResponseWriter, r *http.Request) {
		var id protocol.Hash
		if id.UnmarshalText([]byte(r.PathValue("output"))) != nil {
			http.Error(w, "INVALID_OUTPUT", 400)
			return
		}
		var status redaction.Observation
		err := db.View(func(v state.ReadView) error {
			var err error
			status, err = redaction.Observe(v, blocks, protocol.RepairIdentity(n.Genesis.Network, protocol.OutputID(id)))
			return err
		})
		if err != nil {
			http.Error(w, "OBSERVATION_FAILED", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
	})
	mux.HandleFunc("GET /v3/obligations/{output}", func(w http.ResponseWriter, r *http.Request) {
		var id protocol.Hash
		if id.UnmarshalText([]byte(r.PathValue("output"))) != nil {
			http.Error(w, "INVALID_OUTPUT", 400)
			return
		}
		var ob rules.DirectObligation
		err := db.View(func(v state.ReadView) error {
			var found bool
			var err error
			ob, found, err = state.Load[rules.DirectObligation](v, rules.DirectObligationKey(protocol.OutputID(id)))
			if err == nil && !found {
				return rules.ErrMissing
			}
			return err
		})
		if err != nil {
			http.Error(w, "NOT_FOUND", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(ob)
	})
	// Reading a body is an observation API. Authentication still uses the original
	// BlockID together with the finalized immutable RepairInput.
	mux.HandleFunc("GET /v3/repairs/{output}", func(w http.ResponseWriter, r *http.Request) {
		var id protocol.Hash
		if id.UnmarshalText([]byte(r.PathValue("output"))) != nil {
			http.Error(w, "INVALID_OUTPUT", 400)
			return
		}
		err := db.View(func(v state.ReadView) error {
			task, found, err := redaction.LoadTask(v, protocol.RepairIdentity(n.Genesis.Network, protocol.OutputID(id)))
			if err != nil {
				return err
			}
			if !found {
				return rules.ErrMissing
			}
			return json.NewEncoder(w).Encode(task)
		})
		if err != nil {
			http.Error(w, "NOT_FOUND", 404)
		}
	})
	httpClient := &http.Client{Timeout: 2 * time.Second}
	collect := func(ctx context.Context, path string, command any, accept func([][]chameleon.Contribution) error) error {
		return collectRepairShares(ctx, httpClient, n.CommitteeURLs[:], path, command, accept)
	}

	ctx, cancel := context.WithCancel(parent)
	worker := &repairWorker{db: db, blocks: blocks, policy: policy, index: int(c.Index), head: head, collect: collect, network: n.Genesis.Network,
		submit: func(ctx context.Context, raw []byte) (uint32, error) {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			response, err := client.BroadcastTxSync(ctx, types.Tx(raw))
			if response != nil {
				return response.Code, err
			}
			return 0, err
		},
	}
	decisionDone := make(chan struct{})
	go func() { defer close(decisionDone); worker.runDecisions(ctx) }()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); worker.run(ctx) }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer localDB.Close()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		var cursor []byte
		_ = localDB.View(func(v state.ReadView) error { cursor, _ = v.Get([]byte("cursor")); return nil })
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Completed tasks leave the hot loop via a local cursor; consensus state
				// is untouched by this worker and remains reproducible on replay.
				entries, err := store.Scan(db, state.Key(113), cursor, 8)
				if err != nil {
					slog.Error("repair task scan", "error", err)
					continue
				}
				for _, entry := range entries {
					var id protocol.Hash
					if json.Unmarshal(entry.Value, &id) != nil {
						break
					}
					started := time.Now()
					var installation redaction.Materialization
					err = db.View(func(v state.ReadView) error {
						var err error
						installation, err = redaction.PrepareMaterialization(v, id)
						return err
					})
					if err == nil {
						err = installation.Install(blocks)
					}
					trace("materialize", id, started, err)
					if err != nil {
						slog.Error("repair materialization", "error", err)
						break
					}
					cursor = entry.Key
					err = localDB.Update(func(state.ReadView) ([]state.Change, error) {
						return []state.Change{{Key: []byte("cursor"), Value: cursor}}, nil
					})
					if err != nil {
						slog.Error("repair cursor", "error", err)
						return
					}
				}

			}
		}
	}()
	return func() { cancel(); <-done; <-workerDone; <-decisionDone }, nil
}
