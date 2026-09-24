package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type e7WalletConfig struct {
	Dir, Network, Listen, Gateway string
	Site                          int
	Receivers                     [2]string
}
type e7RunOptions struct {
	Phase                            string
	Rate, Seed, Lanes, Fast, Pending int
	Duration, Drain                  time.Duration
	Chain, Cross                     bool
}
type e7Sample struct {
	e8Sample
	Phase                                              string
	Generation                                         int
	DeliveryNS, ReceiveNS, ReadyQueueNS, SentElapsedNS int64
}
type e7Delivery struct {
	Phase                                                  string
	Index, Lane, Generation, Hop, Sender, Receiver, Parent int
	Output                                                 protocol.Output
	Certificate                                            []byte
}
type e7Report struct {
	Options                                                                    e7RunOptions
	Site                                                                       int
	StartedNS, EndSendNS, FinishedNS                                           int64
	Scheduled, SkippedPacing, NoReady, PendingFull, WorkerFull, ProgressErrors int
	Samples                                                                    []*e7Sample
	ChainNS                                                                    []int64
	Error                                                                      string
}
type e7Location struct {
	Height  int64
	Missing int
}
type e7Wallet struct {
	config      e7WalletConfig
	network     cfg.Network
	policy      rules.DirectPolicy
	db          store.Store
	keys        []ed25519.PrivateKey
	descriptors []protocol.ReceiveDescriptor
	wallets     []*wallet.Wallet
	cal, fuel   [][]state.OriginOutput
	ci, fi      []int
	receipts    *e7Receipts
	mu          sync.Mutex
	report      *e7Report
	active      bool
	ready       *e7Ready
	end         time.Time
	pending     map[int]*e7Sample
	locations   map[protocol.TxID]e7Location
	roots       map[[2]int]time.Time
	next        int
	runWG       sync.WaitGroup
}

func e7WalletCommand(args []string) error {
	f := flag.NewFlagSet("e7-wallet", flag.ContinueOnError)
	path := f.String("config", "", "wallet config")
	if e := f.Parse(args); e != nil {
		return e
	}
	var c e7WalletConfig
	if e := cfg.Read(*path, &c); e != nil {
		return e
	}
	if c.Site < 0 || c.Site > 1 {
		return protocol.ErrRule
	}
	var n cfg.Network
	if e := cfg.Read(c.Network, &n); e != nil {
		return e
	}
	db, e := store.OpenNoSync(filepath.Join(c.Dir, fmt.Sprintf("e7-wallet%d.db", c.Site)), store.Identity{Network: n.ChainID, Role: "e7-wallet", Node: fmt.Sprint(c.Site), Schema: 4})
	if e != nil {
		return e
	}
	group, e := store.NewGroup(db, 256, 64)
	if e != nil {
		db.Close()
		return e
	}
	defer group.Close()
	p, e := n.Direct.Policy(n.Schedule, n.Organizations)
	if e != nil {
		return e
	}
	w := &e7Wallet{config: c, network: n, policy: p, db: group, keys: make([]ed25519.PrivateKey, 64), descriptors: make([]protocol.ReceiveDescriptor, 64), wallets: make([]*wallet.Wallet, 64), cal: make([][]state.OriginOutput, 64), fuel: make([][]state.OriginOutput, 64), ci: make([]int, 64), fi: make([]int, 64), receipts: newE7Receipts(), pending: map[int]*e7Sample{}, locations: map[protocol.TxID]e7Location{}, roots: map[[2]int]time.Time{}}
	indices := map[protocol.PublicKey]int{}
	owners := map[protocol.PublicKey]bool{}
	// Only this site's private keys are loaded. Public descriptors are in genesis.
	for i := 0; i < 64; i++ {
		w.descriptors[i] = n.Genesis.Outputs[2*i].Output.Recipient
		indices[w.descriptors[i].Owner] = i
		if i%2 != c.Site {
			continue
		}
		w.keys[i], e = cfg.PrivateKey(filepath.Join(c.Dir, "keys", "e8", fmt.Sprintf("%d.key", i)))
		if e != nil {
			return e
		}
		owners[w.descriptors[i].Owner] = true
		w.wallets[i], e = wallet.New(group, n.Genesis.Network, w.descriptors[i].Owner, n.Organizations)
		if e != nil {
			return e
		}
	}
	for _, o := range n.Genesis.Outputs {
		i, ok := indices[o.Output.Recipient.Owner]
		if !ok || i%2 != c.Site {
			continue
		}
		if o.Output.Asset == protocol.AssetCAL {
			w.cal[i] = append(w.cal[i], o)
		} else {
			w.fuel[i] = append(w.fuel[i], o)
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	stop, e := w.follow(ctx, cancel, owners)
	if e != nil {
		return e
	}
	defer stop()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(rw http.ResponseWriter, r *http.Request) { io.WriteString(rw, "alive") })
	mux.HandleFunc("/receive", w.receive)
	mux.HandleFunc("/run", func(rw http.ResponseWriter, r *http.Request) {
		var o e7RunOptions
		if e := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&o); e != nil {
			http.Error(rw, e.Error(), 400)
			return
		}
		if (o.Phase != "warm" && o.Phase != "formal") || o.Rate < 2 || o.Rate > 10000 || o.Lanes < 2 || o.Lanes > 256 || o.Lanes%2 != 0 || o.Fast < 1 || o.Fast > 256 || o.Pending < o.Fast || o.Duration <= 0 || o.Drain <= 0 {
			http.Error(rw, "invalid options", 400)
			return
		}
		w.mu.Lock()
		if w.active || len(w.pending) > 0 {
			w.mu.Unlock()
			http.Error(rw, "previous phase not drained", 409)
			return
		}
		w.active = true
		w.report = &e7Report{Options: o, Site: c.Site}
		w.ready = newE7Ready(o.Lanes)
		w.roots = map[[2]int]time.Time{}
		w.end = time.Now().Add(time.Second + o.Duration)
		for lane := c.Site; lane < o.Lanes; lane += 2 {
			w.ready.add(e7Task{Lane: lane, Hop: 1, Owner: lane % 64, Parent: -1, receivedAt: time.Now()})
		}
		w.mu.Unlock()
		w.runWG.Add(1)
		go func() { defer w.runWG.Done(); w.run(ctx, o) }()
		rw.WriteHeader(202)
	})
	mux.HandleFunc("/status", func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		defer w.mu.Unlock()
		m := map[string]any{"Active": w.active, "Pending": len(w.pending)}
		var oldest time.Duration
		for _, s := range w.pending {
			oldest = max(oldest, time.Since(s.sentAt))
		}
		m["OldestPendingNS"] = int64(oldest)
		if w.ready != nil {
			m["Ready"] = w.ready.len()
		}
		if w.report != nil {
			m["Samples"] = len(w.report.Samples)
			if !w.active {
				m["Error"] = w.report.Error
			}
		}
		json.NewEncoder(rw).Encode(m)
	})
	server, e := cfg.HTTP(c.Listen, mux, cfg.TLS{})
	if e != nil {
		return e
	}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		server.Shutdown(shutdown)
	}()
	e = cfg.Serve(server)
	cancel()
	w.runWG.Wait()
	return e
}

func (w *e7Wallet) receive(rw http.ResponseWriter, r *http.Request) {
	var d e7Delivery
	if e := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&d); e != nil {
		http.Error(rw, e.Error(), 400)
		return
	}
	if d.Index < 0 || d.Receiver < 0 || d.Receiver >= 64 || d.Receiver%2 != w.config.Site || d.Hop < 1 || d.Hop > 10 {
		http.Error(rw, "invalid recipient", 400)
		return
	}
	cert, e := protocol.DecodeOutputCertificate(d.Certificate)
	if e != nil {
		http.Error(rw, e.Error(), 400)
		return
	}
	ack, e := w.receipts.accept(d.Index, func() (e7Ack, error) {
		start := time.Now()
		if e := w.wallets[d.Receiver].ReceiveDirect(d.Output, cert, 0); e != nil {
			return e7Ack{}, e
		}
		elapsed := int64(time.Since(start))
		now := time.Now()
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.report != nil && w.report.Options.Phase == d.Phase && w.report.Options.Chain {
			if d.Hop == 10 {
				if start, ok := w.roots[[2]int{d.Lane, d.Generation}]; ok {
					w.report.ChainNS = append(w.report.ChainNS, int64(now.Sub(start)))
					delete(w.roots, [2]int{d.Lane, d.Generation})
				}
			}
			if w.active && now.Before(w.end) {
				task := e7Task{Lane: d.Lane, Generation: d.Generation, Hop: d.Hop + 1, Owner: d.Receiver, Parent: d.Index, Input: protocol.OutputIdentity(w.network.Genesis.Network, cert.Summary.Tx, 0), receivedAt: now}
				if d.Hop == 10 {
					task = e7Task{Lane: d.Lane, Generation: d.Generation + 1, Hop: 1, Owner: d.Lane % 64, Parent: -1, receivedAt: now}
				}
				if !w.ready.add(task) {
					return e7Ack{}, fmt.Errorf("continuation queue rejected lane %d hop %d", task.Lane, task.Hop)
				}
			}
		}
		return e7Ack{Index: d.Index, ReceiveNS: elapsed, Fact: cert.QC.Fact}, nil
	})
	if e != nil {
		http.Error(rw, e.Error(), 400)
		return
	}
	json.NewEncoder(rw).Encode(ack)
}

func e7SendReceipt(ctx context.Context, client *http.Client, url string, d e7Delivery) (e7Ack, error) {
	raw, e := json.Marshal(d)
	if e != nil {
		return e7Ack{}, e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, url+"/receive", bytes.NewReader(raw))
	if e != nil {
		return e7Ack{}, e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := client.Do(req)
	if e != nil {
		return e7Ack{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return e7Ack{}, fmt.Errorf("receive %d: %s", resp.StatusCode, b)
	}
	var a e7Ack
	e = json.NewDecoder(resp.Body).Decode(&a)
	if e == nil && a.Index != d.Index {
		e = fmt.Errorf("receipt identity mismatch")
	}
	return a, e
}
