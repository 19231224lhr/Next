package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtcfg "github.com/cometbft/cometbft/config"
	cmted "github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/p2p"
	"github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/proxy"
	"github.com/cometbft/cometbft/rpc/client/local"
	ct "github.com/cometbft/cometbft/types"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/committee"
	"utxo/internal/requesttrace"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/protocol"
)

type configuration struct {
	Network, DataDir, KeyFile, P2PListen, Peers, Listen string
	RepairKeyFile                                       string
	Index                                               uint16
	TLS                                                 cfg.TLS
}

func run() error {
	path := flag.String("config", "", "committee config JSON")
	flag.Parse()
	var c configuration
	if e := cfg.Read(*path, &c); e != nil {
		return e
	}
	var network cfg.Network
	if e := cfg.Read(c.Network, &network); e != nil {
		return e
	}
	if c.Index >= 4 {
		return protocol.ErrRule
	}
	key, e := cfg.PrivateKey(c.KeyFile)
	if e != nil {
		return e
	}
	if !bytes.Equal(key[32:], network.Committee[c.Index][:]) {
		return protocol.ErrAuth
	}
	if _, e = network.Trust(); e != nil {
		return e
	}
	db, e := store.Open(filepath.Join(c.DataDir, "committee.db"), store.Identity{Network: network.Genesis.Network.String(), Role: "committee", Node: fmt.Sprint(c.Index), Schema: network.Schema()})
	if e != nil {
		return e
	}
	defer db.Close()
	engine, e := committee.NewEngine(network.Engine(), db)
	if e != nil {
		return e
	}
	if e = configureDirect(network, engine); e != nil {
		return e
	}
	var app *committee.App
	if network.Direct != nil {
		app, e = committee.NewTimedApp(network.ChainID, db, engine.Check, engine.ExecuteAt, engine.BeginBlock)
	} else {
		app, e = committee.NewApp(network.ChainID, db, engine.Check, engine.Execute, engine.Drain)
	}
	if e != nil {
		return e
	}
	cc := cmtcfg.DefaultConfig().SetRoot(filepath.Join(c.DataDir, "comet"))
	cmtcfg.EnsureRoot(cc.RootDir)
	cc.Moniker = fmt.Sprintf("committee-%d", c.Index)
	cc.RPC.ListenAddress = ""
	cc.P2P.ListenAddress = c.P2PListen
	cc.P2P.PersistentPeers = c.Peers
	cc.P2P.AllowDuplicateIP = true
	cc.P2P.AddrBookStrict = false
	cc.P2P.PexReactor = false
	cc.Consensus.TimeoutCommit = 100 * time.Millisecond
	// Wait when idle; CometBFT still produces blocks needed to authenticate AppHash changes.
	cc.Consensus.CreateEmptyBlocks = false
	cc.Consensus.CreateEmptyBlocksInterval = 0
	// Diagnostic A/B overrides only; defaults and consensus durability are unchanged.
	if value := os.Getenv("UTXO_EXPERIMENT_FLUSH"); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			return fmt.Errorf("invalid UTXO_EXPERIMENT_FLUSH")
		}
		cc.P2P.FlushThrottleTimeout = d
	}
	if value := os.Getenv("UTXO_EXPERIMENT_GOSSIP"); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			return fmt.Errorf("invalid UTXO_EXPERIMENT_GOSSIP")
		}
		cc.Consensus.PeerGossipSleepDuration = d
	}
	requesttrace.Consensus.Mark("configuration", "flush", cc.P2P.FlushThrottleTimeout, "gossip", cc.Consensus.PeerGossipSleepDuration, "commit", cc.Consensus.TimeoutCommit)
	cc.StateSync.Enable = false
	var validator *privval.FilePV
	_, keyErr := os.Stat(cc.PrivValidatorKeyFile())
	_, stateErr := os.Stat(cc.PrivValidatorStateFile())
	if os.IsNotExist(keyErr) && os.IsNotExist(stateErr) {
		validator = privval.NewFilePV(cmted.PrivKey(key), cc.PrivValidatorKeyFile(), cc.PrivValidatorStateFile())
		validator.Save()
	} else if keyErr != nil || stateErr != nil {
		return fmt.Errorf("incomplete consensus signing state: key=%v state=%v", keyErr, stateErr)
	} else {
		validator = privval.LoadFilePV(cc.PrivValidatorKeyFile(), cc.PrivValidatorStateFile())
	}
	public, e := validator.GetPubKey()
	if e != nil {
		return e
	}
	if !bytes.Equal(public.Bytes(), network.Committee[c.Index][:]) {
		return protocol.ErrAuth
	}
	nk, e := p2p.LoadNodeKey(cc.NodeKeyFile())
	if e != nil {
		return e
	}
	genesis := &ct.GenesisDoc{ChainID: network.ChainID, GenesisTime: network.GenesisTime, InitialHeight: 1, ConsensusParams: ct.DefaultConsensusParams()}
	genesisRoot := engine.GenesisHash()
	genesis.AppHash = genesisRoot[:]
	for i, p := range network.Committee {
		public := cmted.PubKey(bytes.Clone(p[:]))
		genesis.Validators = append(genesis.Validators, ct.GenesisValidator{Address: public.Address(), PubKey: public, Power: 1, Name: fmt.Sprint(i)})
	}
	logger := log.NewFilter(log.NewTMLogger(log.NewSyncWriter(os.Stderr)), log.AllowError())
	logger = requesttrace.Consensus.Logger(logger)
	consensus, e := node.NewNode(cc, validator, nk, proxy.NewLocalClientCreator(app), func() (*ct.GenesisDoc, error) { return genesis, nil }, cmtcfg.DefaultDBProvider, node.DefaultMetricsProvider(cc.Instrumentation), logger)
	if e != nil {
		return e
	}
	if e = consensus.Start(); e != nil {
		return e
	}
	defer func() { consensus.Stop(); consensus.Wait() }()
	client := local.New(consensus)
	mux := http.NewServeMux()
	if network.Direct != nil {
		mux.HandleFunc("GET /v3/receipts/{spend}", directReceiptsHandler(app, engine, client))
	}
	mux.HandleFunc("POST /v1/commands", func(w http.ResponseWriter, r *http.Request) {
		var received int64
		if requesttrace.Settlement != nil {
			received = time.Now().UnixNano()
		}
		if r.Header.Get("Content-Type") != transport.MediaType {
			http.Error(w, "INVALID_ENCODING", 400)
			return
		}
		limit := int64(protocol.MaxCertificateBytes + 128)
		if network.Direct != nil {
			limit = protocol.MaxRequestBytes
		}
		raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
		if e != nil {
			http.Error(w, "INVALID_ENCODING", 400)
			return
		}
		if requesttrace.Settlement != nil {
			sent, _ := strconv.ParseInt(r.Header.Get(requesttrace.DeliveryTimeHeader), 10, 64)
			requesttrace.Settlement.Receive(raw, sent, received, r.Header.Get(requesttrace.DeliverySourceHeader))
		}
		result, e := client.BroadcastTxSync(r.Context(), ct.Tx(raw))
		if e != nil {
			http.Error(w, "SUBMISSION_FAILED", 503)
			return
		}
		if result.Code != 0 {
			http.Error(w, "INVALID_COMMAND", 400)
			return
		}
		requesttrace.Settlement.Command(raw, "accepted", 0)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"command": fmt.Sprintf("%X", result.Hash), "status": "SUBMITTED"})
	})
	mux.HandleFunc("GET /v1/receipts/{kind}/{key}", func(w http.ResponseWriter, r *http.Request) {
		kind, e := strconv.ParseUint(r.PathValue("kind"), 10, 16)
		if e != nil {
			http.Error(w, "INVALID_KIND", 400)
			return
		}
		var key protocol.Hash
		if e = key.UnmarshalText([]byte(r.PathValue("key"))); e != nil {
			http.Error(w, "INVALID_KEY", 400)
			return
		}
		fact, e := app.LatestFact(protocol.FactKind(kind), key)
		if e != nil {
			http.Error(w, "DEFERRED", 404)
			return
		}
		height, e := app.FactHeight(fact.ID())
		if e != nil {
			http.Error(w, "PROOF_PENDING", 503)
			return
		}
		height++
		commit, e := client.Commit(r.Context(), &height)
		if e != nil {
			http.Error(w, "PROOF_PENDING", 503)
			return
		}
		proof, e := app.Proof(fact.ID(), commit.SignedHeader)
		if e != nil {
			http.Error(w, "PROOF_PENDING", 503)
			return
		}
		raw, e := proof.MarshalBinary()
		if e != nil {
			http.Error(w, "PROOF_UNAVAILABLE", 500)
			return
		}
		w.Header().Set("Content-Type", transport.MediaType)
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("GET /v1/outcomes/{spend}", func(w http.ResponseWriter, r *http.Request) {
		var id protocol.Hash
		if e := id.UnmarshalText([]byte(r.PathValue("spend"))); e != nil {
			http.Error(w, "INVALID_ID", 400)
			return
		}
		p, e := engine.Payment(protocol.SpendFactID(id))
		if e != nil {
			http.Error(w, "DEFERRED", 404)
			return
		}
		phase := "REGISTERED"
		if p.Settled {
			phase = "SETTLED"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			PublicPhase string
			FeeClosed   bool
			Paid        string
			ProofStatus string
		}{phase, p.Fee.Closed, strconv.FormatUint(p.Fee.Rewards+p.Fee.Burned, 10), "PENDING_CLIENT_VERIFICATION"})
	})
	mux.HandleFunc("GET /v1/txcers/{spend}", func(w http.ResponseWriter, r *http.Request) {
		var id protocol.Hash
		if e := id.UnmarshalText([]byte(r.PathValue("spend"))); e != nil {
			http.Error(w, "INVALID_ID", 400)
			return
		}
		p, e := engine.Payment(protocol.SpendFactID(id))
		if e != nil {
			http.Error(w, "DEFERRED", 404)
			return
		}
		w.Header().Set("Content-Type", transport.MediaType)
		_, _ = w.Write(p.Certificate)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		info, _ := app.Info(r.Context(), &abci.RequestInfo{})
		_ = json.NewEncoder(w).Encode(map[string]string{"height": strconv.FormatInt(info.LastBlockHeight, 10)})
	})
	if requesttrace.Settlement != nil {
		mux.HandleFunc("GET /debug/consensus", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(requesttrace.Consensus.Snapshot())
		})
		mux.HandleFunc("GET /debug/settlement/{spend}", func(w http.ResponseWriter, r *http.Request) {
			var id protocol.Hash
			if err := id.UnmarshalText([]byte(r.PathValue("spend"))); err != nil {
				http.Error(w, "INVALID_ID", 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(requesttrace.Settlement.ForSpend(id.String()))
		})
	}
	server, e := cfg.HTTP(c.Listen, mux, c.TLS)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	repairStop, e := startRepairRuntime(ctx, c, network, db, app, consensus, mux)
	if e != nil {
		return e
	}
	defer repairStop()
	done := make(chan error, 1)
	go func() { done <- cfg.Serve(server) }()
	slog.Info("committee started", "listen", c.Listen, "index", c.Index)
	select {
	case e = <-done:
		return e
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
func main() {
	if e := run(); e != nil {
		slog.Error("committee stopped", "error", e)
		os.Exit(1)
	}
}
