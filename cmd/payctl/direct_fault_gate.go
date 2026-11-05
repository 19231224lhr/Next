package main

// E4-only loopback proxy. A blocked request is rejected, never queued for release.
import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/transport"
)

type faultGateRule struct {
	Block   []string
	DelayMS map[string]int
}
type faultGateCount struct {
	Enter, Blocked, Forwarded, OK, Errors, Delayed, Cancelled int
	LastForwardNS, LastOKNS                                   int64
}
type faultGate struct {
	mu      sync.Mutex
	targets []string
	client  *http.Client
	rule    faultGateRule
	counts  map[string]*faultGateCount
}

func newFaultGate(targets []string) *faultGate {
	return &faultGate{targets: targets, client: transport.NewHTTPClient(2 * time.Second), counts: map[string]*faultGateCount{}}
}
func (g *faultGate) record(key string, fn func(*faultGateCount)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.counts[key]
	if c == nil {
		c = &faultGateCount{}
		g.counts[key] = c
	}
	fn(c)
}
func (g *faultGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/control" {
		if r.Method == "POST" {
			var rule faultGateRule
			if json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&rule) != nil {
				http.Error(w, "bad control", 400)
				return
			}
			g.mu.Lock()
			g.rule = rule
			g.mu.Unlock()
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Rule   faultGateRule
			Counts map[string]*faultGateCount
		}{g.rule, g.counts})
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/")
	part, path, ok := strings.Cut(key, "/")
	i, err := strconv.Atoi(part)
	if !ok || err != nil || i < 0 || i >= len(g.targets) {
		http.NotFound(w, r)
		return
	}
	g.mu.Lock()
	rule := g.rule
	g.mu.Unlock()
	blocked := false
	for _, b := range rule.Block {
		if b == key {
			blocked = true
			break
		}
	}
	g.record(key, func(c *faultGateCount) {
		c.Enter++
		if blocked {
			c.Blocked++
		}
	})
	if blocked {
		http.Error(w, "E4_BLOCKED_BEFORE_FORWARD", 503)
		return
	}
	target, err := url.Parse(g.targets[i] + "/" + path)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	target.RawQuery = r.URL.RawQuery
	req := r.Clone(r.Context())
	req.URL = target
	req.RequestURI = ""
	req.Host = target.Host
	g.record(key, func(c *faultGateCount) { c.Forwarded++; c.LastForwardNS = time.Now().UnixNano() })
	resp, err := g.client.Do(req)
	if err != nil {
		g.record(key, func(c *faultGateCount) { c.Errors++ })
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	g.record(key, func(c *faultGateCount) {
		if resp.StatusCode == 200 || resp.StatusCode == 202 {
			c.OK++
			c.LastOKNS = time.Now().UnixNano()
		} else {
			c.Errors++
		}
	})
	if delay := rule.DelayMS[key]; delay > 0 {
		g.record(key, func(c *faultGateCount) { c.Delayed++ })
		timer := time.NewTimer(time.Duration(delay) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			g.record(key, func(c *faultGateCount) { c.Cancelled++ })
			return
		}
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(raw)
}
func faultProxy(args []string) error {
	f := flag.NewFlagSet("fault-proxy", flag.ContinueOnError)
	dir := f.String("dir", "", "lab")
	listen := f.String("listen", "127.0.0.1:29000", "loopback control/proxy")
	if err := f.Parse(args); err != nil {
		return err
	}
	var lab cfg.Lab
	var n cfg.Network
	if err := cfg.Read(filepath.Join(*dir, "lab.json"), &lab); err != nil {
		return err
	}
	if err := cfg.Read(lab.Network, &n); err != nil {
		return err
	}
	members := n.Members[n.Organizations[0].Org]
	targets := append(members[:], n.CommitteeURLs[:]...)
	server, err := cfg.HTTP(*listen, newFaultGate(targets), cfg.TLS{})
	if err != nil {
		return err
	}
	fmt.Println("E4 proxy ready")
	return server.ListenAndServe()
}
