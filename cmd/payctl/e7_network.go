package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/transport"
)

type e7Target struct {
	Name, URL string
	Site      int
}
type e7ProxyConfig struct {
	Targets []e7Target
	RTT     time.Duration
}
type e7LinkCount struct {
	Requests, Forwarded, Completed, Cancelled, Errors, Active, Peak int
	RequestBytes, ResponseBytes, DelayNS                            int64
}
type e7Proxy struct {
	config e7ProxyConfig
	client *http.Client
	mu     sync.Mutex
	counts map[string]*e7LinkCount
}

func newE7Proxy(targets []e7Target, rtt time.Duration) *e7Proxy {
	return &e7Proxy{config: e7ProxyConfig{targets, rtt}, client: transport.NewHTTPClient(15 * time.Second), counts: map[string]*e7LinkCount{}}
}
func (p *e7Proxy) record(key string, f func(*e7LinkCount)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.counts[key]
	if c == nil {
		c = &e7LinkCount{}
		p.counts[key] = c
	}
	f(c)
}
func (p *e7Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/stats" {
		p.mu.Lock()
		defer p.mu.Unlock()
		json.NewEncoder(w).Encode(p.counts)
		return
	}
	if r.URL.Path == "/healthz" {
		io.WriteString(w, "alive")
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 3)
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}
	source, e := strconv.Atoi(parts[0])
	target, te := strconv.Atoi(parts[1])
	if e != nil || te != nil || source < 0 || source > 2 || target < 0 || target >= len(p.config.Targets) {
		http.NotFound(w, r)
		return
	}
	t := p.config.Targets[target]
	delay := time.Duration(0)
	if source != t.Site {
		delay = p.config.RTT / 2
	}
	key := fmt.Sprintf("%d/%d/%s", source, target, strings.Join(strings.Split(parts[2], "/")[:min(2, len(strings.Split(parts[2], "/")))], "/"))
	p.record(key, func(c *e7LinkCount) {
		c.Requests++
		c.Active++
		c.Peak = max(c.Peak, c.Active)
		c.RequestBytes += max(0, r.ContentLength)
	})
	defer p.record(key, func(c *e7LinkCount) { c.Active-- })
	pause := func() bool {
		start := time.Now()
		err := budgetPause(r.Context(), delay)
		p.record(key, func(c *e7LinkCount) {
			c.DelayNS += int64(time.Since(start))
			if err != nil {
				c.Cancelled++
			}
		})
		return err == nil
	}
	if !pause() {
		return
	}
	u, _ := url.Parse(t.URL + "/" + parts[2])
	u.RawQuery = r.URL.RawQuery
	req := r.Clone(r.Context())
	req.URL = u
	req.Host = u.Host
	req.RequestURI = ""
	p.record(key, func(c *e7LinkCount) { c.Forwarded++ })
	resp, e := p.client.Do(req)
	if e != nil {
		p.record(key, func(c *e7LinkCount) { c.Errors++ })
		http.Error(w, e.Error(), 502)
		return
	}
	defer resp.Body.Close()
	// Delay once before response delivery, never once per body chunk.
	if !pause() {
		return
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	n, e := io.Copy(w, resp.Body)
	p.record(key, func(c *e7LinkCount) {
		c.ResponseBytes += n
		if e != nil || resp.StatusCode >= 400 {
			c.Errors++
		} else {
			c.Completed++
		}
	})
}
func e7ProxyCommand(args []string) error {
	f := flag.NewFlagSet("e7-proxy", flag.ContinueOnError)
	path := f.String("config", "", "proxy configuration")
	listen := f.String("listen", "127.0.0.1:29100", "loopback listen")
	if e := f.Parse(args); e != nil {
		return e
	}
	var c e7ProxyConfig
	if e := cfg.Read(*path, &c); e != nil {
		return e
	}
	if c.RTT < 0 || c.RTT > time.Second {
		return fmt.Errorf("invalid delay")
	}
	s, e := cfg.HTTP(*listen, newE7Proxy(c.Targets, c.RTT), cfg.TLS{})
	if e != nil {
		return e
	}
	return cfg.Serve(s)
}
