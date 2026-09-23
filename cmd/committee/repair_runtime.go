//go:build comet_v3

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/rpc/client/local"
	"github.com/cometbft/cometbft/types"
	"io"
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
	slots := make(chan struct{}, 2)
	handler := func(parts bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			default:
				http.Error(w, "BUSY", 429)
				return
			}
			_, now := head()
			var command protocol.RepairInput
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, protocol.MaxRequestBytes))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&command); err != nil {
				http.Error(w, "INVALID_REQUEST", 400)
				return
			}
			var response [][]byte
			err := db.View(func(v state.ReadView) error {
				var shares []chameleon.Contribution
				if parts {
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
	mux.HandleFunc("POST /v3/repair/input-share", handler(false))
	mux.HandleFunc("POST /v3/repair/part-shares", handler(true))
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
			task, found, err := state.Load[redaction.Task](v, redaction.TaskKey(protocol.RepairIdentity(n.Genesis.Network, protocol.OutputID(id))))
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
	collect := func(ctx context.Context, path string, command protocol.RepairInput) ([][]chameleon.Contribution, error) {
		raw, err := json.Marshal(command)
		if err != nil {
			return nil, err
		}
		type result struct {
			shares []chameleon.Contribution
			err    error
		}
		responses := make(chan result, 4)
		for _, url := range n.CommitteeURLs {
			go func(url string) {
				req, err := http.NewRequestWithContext(ctx, "POST", url+path, bytes.NewReader(raw))
				if err != nil {
					responses <- result{err: err}
					return
				}
				req.Header.Set("Content-Type", "application/json")
				resp, err := httpClient.Do(req)
				if err != nil {
					responses <- result{err: err}
					return
				}
				defer resp.Body.Close()
				if resp.StatusCode != 200 {
					responses <- result{err: rules.ErrMissing}
					return
				}
				var wire [][]byte
				err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&wire)
				var shares []chameleon.Contribution
				if err == nil {
					for _, b := range wire {
						s, e := chameleon.DecodeContribution(b)
						if e != nil {
							err = e
							break
						}
						shares = append(shares, s)
					}
				}
				responses <- result{shares: shares, err: err}
			}(url)
		}
		var all [][]chameleon.Contribution
		for i := 0; i < 4; i++ {
			r := <-responses
			if r.err == nil {
				all = append(all, r.shares)
			}
		}
		if len(all) < 3 {
			return nil, protocol.ErrAuth
		}
		return all, nil
	}
	ctx, cancel := context.WithCancel(parent)
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
					err = db.View(func(v state.ReadView) error { return redaction.Materialize(v, blocks, id) })
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
				due, err := store.Scan(db, state.Key(112), nil, 8)
				if err != nil {
					continue
				}
				for _, entry := range due {
					var ob rules.DirectObligation
					if json.Unmarshal(entry.Value, &ob) != nil {
						continue
					}
					if time.Now().Unix() < ob.Deadline+int64(c.Index) {
						break
					}
					height, now := head()
					if now < ob.Deadline {
						_, _ = client.BroadcastTxSync(ctx, types.Tx(protocol.ClockTick(n.Genesis.Network, height+1)))
						break
					}
					var command protocol.RepairInput
					var payment protocol.DirectSubmission
					err = db.View(func(v state.ReadView) error {
						var err error
						command, payment, err = redaction.InputTarget(v, blocks, policy, ob.Output, now)
						return err
					})
					if err != nil {
						continue
					}
					id := protocol.RepairIdentity(n.Genesis.Network, ob.Output)
					started := time.Now()
					inputVotes, err := collect(ctx, "/v3/repair/input-share", command)
					trace("input_shares", id, started, err, "output", protocol.Hash(ob.Output).String(), "deadline", ob.Deadline)
					if err != nil {
						continue
					}
					var inputShares []chameleon.Contribution
					for _, votes := range inputVotes {
						if len(votes) == 1 {
							inputShares = append(inputShares, votes[0])
						}
					}
					command, err = redaction.ReplaceInput(policy, command, payment, inputShares)
					if err != nil {
						continue
					}
					started = time.Now()
					partVotes, err := collect(ctx, "/v3/repair/part-shares", command)
					trace("part_shares", id, started, err)
					if err != nil {
						continue
					}
					err = db.View(func(v state.ReadView) error {
						var err error
						command, err = redaction.CompleteParts(v, blocks, policy, command, now, partVotes)
						return err
					})
					if err != nil {
						continue
					}
					raw, err := command.MarshalBinary()
					if err != nil {
						continue
					}
					started = time.Now()
					response, err := client.BroadcastTxSync(ctx, types.Tx(raw))
					code := uint32(0)
					if response != nil {
						code = response.Code
					}
					trace("submit", id, started, err, "code", code, "bytes", len(raw), "target_height", command.Height, "base_revision", command.Base)
				}
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}
