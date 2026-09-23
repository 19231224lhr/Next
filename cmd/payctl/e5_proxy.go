package main

// E5 changes response delivery only. The gateway finishes its original response
// before the proxy waits; INSTALL and public submission remain unmodified.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/protocol"
)

type e5ProxyConfig struct {
	Gateway string
	Members [4]string
}
type e5Timing struct {
	Tx                            protocol.TxID
	RegisteredNS, QCNS, ReleaseNS int64
	Attempts                      int
	InstallCalls, AlreadyObserved [4]int
}
type e5Proxy struct {
	gate    *e5Gate
	config  e5ProxyConfig
	network cfg.Network
	mode    string
	client  *http.Client
	mu      sync.Mutex
	times   map[e5RequestKey]*e5Timing
}

func e5Identity(n protocol.Hash, req protocol.DirectRequest) (e5RequestKey, [32]byte, error) {
	raw, err := req.MarshalBinary()
	return e5RequestKey{n, req.Tx.ID()}, sha256.Sum256(raw), err
}

func (p *e5Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/stats" {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.gate.mu.Lock()
		defer p.gate.mu.Unlock()
		type row struct {
			e5Timing
			Install3NS, PublicNS int64
			Stored               uint8
			StoredNS             [4]int64
			Reason               string
		}
		rows := make([]row, 0, len(p.times))
		for k, t := range p.times {
			e := p.gate.entries[k]
			_, reason := e.ready()
			v := row{e5Timing: *t, Stored: e.stored, StoredNS: e.storedAt, Reason: reason}
			if !e.install3At.IsZero() {
				v.Install3NS = e.install3At.UnixNano()
			}
			if !e.publicAt.IsZero() {
				v.PublicNS = e.publicAt.UnixNano()
			}
			rows = append(rows, v)
		}
		_ = json.NewEncoder(w).Encode(rows)
		return
	}
	var target, path string
	index := -1
	if strings.HasPrefix(r.URL.Path, "/gateway/") {
		target = p.config.Gateway
		path = strings.TrimPrefix(r.URL.Path, "/gateway")
	} else if strings.HasPrefix(r.URL.Path, "/member/") {
		part, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/member/"), "/")
		i, e := strconv.Atoi(part)
		if !ok || e != nil || i < 0 || i >= 4 {
			http.NotFound(w, r)
			return
		}
		index = i
		target = p.config.Members[i]
		path = "/" + rest
	} else {
		http.NotFound(w, r)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	isPayment := index < 0 && path == "/v3/transactions" && r.Method == "POST"
	var key e5RequestKey
	if isPayment {
		req, e := protocol.DecodeDirectRequest(raw)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		var digest [32]byte
		key, digest, e = e5Identity(p.network.Genesis.Network, req)
		if e == nil {
			_, e = p.gate.Register(key, digest)
		}
		if e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		p.mu.Lock()
		t := p.times[key]
		if t == nil {
			t = &e5Timing{Tx: key.Tx, RegisteredNS: time.Now().UnixNano()}
			p.times[key] = t
		}
		t.Attempts++
		p.mu.Unlock()
	}
	u, err := url.Parse(target + path)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	u.RawQuery = r.URL.RawQuery
	out := r.Clone(r.Context())
	out.URL = u
	out.Host = u.Host
	out.RequestURI = ""
	out.Body = io.NopCloser(bytes.NewReader(raw))
	out.ContentLength = int64(len(raw))
	if index >= 0 && path == "/v3/certificates" {
		out.Header.Set("X-E5-Observe-Install", "1")
		payment, e := protocol.DecodeDirectPayment(raw)
		if e == nil {
			k := e5RequestKey{p.network.Genesis.Network, payment.Tx.ID()}
			p.mu.Lock()
			if t := p.times[k]; t != nil {
				t.InstallCalls[index]++
			}
			p.mu.Unlock()
		}
	}
	resp, err := p.client.Do(out)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	resp.Body.Close()
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	if index >= 0 && path == "/v3/certificates" && resp.StatusCode == 200 && resp.Header.Get("X-E5-Install-State") == "already_observed" {
		payment, e := protocol.DecodeDirectPayment(raw)
		if e == nil {
			k := e5RequestKey{p.network.Genesis.Network, payment.Tx.ID()}
			p.mu.Lock()
			if t := p.times[k]; t != nil {
				t.AlreadyObserved[index]++
			}
			p.mu.Unlock()
		}
	}
	if index >= 0 && path == "/v3/certificates" && resp.StatusCode == 200 && resp.Header.Get("X-E5-Install-State") == "stored" {
		payment, e := protocol.DecodeDirectPayment(raw)
		if e == nil {
			e = payment.Certificate.Verify(p.network.Organizations[0])
		}
		if e == nil {
			k, d, e := e5Identity(p.network.Genesis.Network, protocol.DirectRequest{Tx: payment.Tx, InputCertificates: payment.InputCertificates})
			if e == nil {
				e = p.gate.Stored(k, d, payment.Certificate.QC.Fact, uint16(index))
			}
			if e != nil {
				http.Error(w, e.Error(), 500)
				return
			}
		} else {
			http.Error(w, e.Error(), 500)
			return
		}
	}
	if isPayment && resp.StatusCode == 200 {
		cert, e := protocol.DecodeOutputCertificate(body)
		if e == nil {
			e = cert.Verify(p.network.Organizations[0])
		}
		if e == nil && cert.Summary.Tx != key.Tx {
			e = protocol.ErrAuth
		}
		if e == nil {
			e = p.gate.Bind(key, cert.QC.Fact)
		}
		if e != nil {
			http.Error(w, e.Error(), 502)
			return
		}
		p.mu.Lock()
		if p.times[key].QCNS == 0 {
			p.times[key].QCNS = time.Now().UnixNano()
		}
		p.mu.Unlock()
		if p.mode == "B" {
			if _, e = p.gate.Wait(r.Context(), key); e != nil {
				return
			}
		}
		p.mu.Lock()
		if p.times[key].ReleaseNS == 0 {
			p.times[key].ReleaseNS = time.Now().UnixNano()
		}
		p.mu.Unlock()
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

func (p *e5Proxy) follow(ctx context.Context, cancel context.CancelFunc) func() {
	db := store.NewMemory()
	trust, _ := p.network.Trust()
	type result struct {
		key    e5RequestKey
		digest [32]byte
		fact   protocol.SpendFactID
	}
	var pending []result
	stop := blockfollow.Start(ctx, cancel, db, transport.NewCommitteeClient(p.network.CommitteeURLs[0]), trust, func(b finality.VerifiedBlock) (blockfollow.Apply, error) {
		pending = nil
		for _, t := range b.Transactions() {
			if t.Code != 0 || len(t.Data) == 0 {
				continue
			}
			r, e := protocol.DecodeExecution(t.Data)
			if e != nil {
				return nil, e
			}
			if !r.Applied {
				continue
			}
			s, e := protocol.DecodeDirectSubmission(t.Bytes)
			if e != nil {
				continue
			}
			k, d, e := e5Identity(p.network.Genesis.Network, protocol.DirectRequest{Tx: s.Tx, InputCertificates: s.InputCertificates})
			if e != nil {
				return nil, e
			}
			p.gate.mu.Lock()
			registered := p.gate.entries[k] != nil
			p.gate.mu.Unlock()
			if registered {
				pending = append(pending, result{k, d, s.Authorization.Fact})
			}
		}
		return func(*state.Overlay) error { return nil }, nil
	}, func(int64) {
		for _, r := range pending {
			if e := p.gate.Public(r.key, r.digest, r.fact); e != nil {
				fmt.Fprintln(os.Stderr, e)
				cancel()
			}
		}
	})
	return func() { stop(); db.Close() }
}

func e5ProxyCommand(args []string) error {
	f := flag.NewFlagSet("e5-proxy", flag.ContinueOnError)
	dir := f.String("dir", "", "lab")
	listen := f.String("listen", "127.0.0.1:29000", "loopback proxy")
	mode := f.String("mode", "A", "A or B")
	setup := f.Bool("setup", false, "wire fresh lab through proxy")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *mode != "A" && *mode != "B" {
		return fmt.Errorf("mode must be A or B")
	}
	var lab cfg.Lab
	var n cfg.Network
	if e := cfg.Read(filepath.Join(*dir, "lab.json"), &lab); e != nil {
		return e
	}
	if e := cfg.Read(lab.Network, &n); e != nil {
		return e
	}
	manifest := filepath.Join(*dir, "e5-proxy.json")
	var c e5ProxyConfig
	if *setup {
		if _, e := os.Stat(manifest); e == nil {
			return fmt.Errorf("proxy already configured")
		}
		c = e5ProxyConfig{lab.Gateways[0], n.Members[n.Organizations[0].Org]}
		if e := cfg.Write(manifest, c); e != nil {
			return e
		}
		base := "http://" + *listen
		lab.Gateways[0] = base + "/gateway"
		var members [4]string
		for i := range members {
			members[i] = fmt.Sprintf("%s/member/%d", base, i)
		}
		n.Members[n.Organizations[0].Org] = members
		for _, node := range lab.Nodes {
			if node.URL == c.Gateway {
				raw, e := os.ReadFile(node.Config)
				if e != nil {
					return e
				}
				var obj map[string]any
				if e = json.Unmarshal(raw, &obj); e != nil {
					return e
				}
				obj["Members"] = members
				if e = cfg.Write(node.Config, obj); e != nil {
					return e
				}
			}
		}
		raw, e := json.Marshal(n)
		if e != nil {
			return e
		}
		if e := os.WriteFile(lab.Network, raw, 0600); e != nil {
			return e
		}
		return cfg.Write(filepath.Join(*dir, "lab.json"), lab)
	}
	if e := cfg.Read(manifest, &c); e != nil {
		return e
	}
	p := &e5Proxy{gate: newE5Gate(30100), config: c, network: n, mode: *mode, client: transport.NewHTTPClient(2 * time.Second), times: map[e5RequestKey]*e5Timing{}}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	stop := p.follow(ctx, cancel)
	defer stop()
	server := &http.Server{Addr: *listen, Handler: p, ReadHeaderTimeout: 3 * time.Second}
	go func() {
		<-ctx.Done()
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = server.Shutdown(c)
	}()
	e := server.ListenAndServe()
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
