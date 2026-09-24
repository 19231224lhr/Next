package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestE7RoutesAndDependencies(t *testing.T) {
	for _, cross := range []bool{false, true} {
		for owner := 0; owner < 64; owner++ {
			next := e7Recipient(owner, 64, 23, cross)
			if next == owner || (next%2 != owner%2) != cross {
				t.Fatalf("route %d -> %d", owner, next)
			}
		}
	}
	q := newE7Ready(64)
	task := e7Task{Lane: 1, Generation: 2, Hop: 2, Owner: 3, Parent: 9}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); q.add(task) }()
	}
	wg.Wait()
	if _, ok := q.take(); !ok {
		t.Fatal("missing received task")
	}
	if _, ok := q.take(); ok {
		t.Fatal("duplicate continuation")
	}
	q.add(e7Task{Lane: 1, Generation: 2, Hop: 3})
	if _, ok := q.take(); !ok {
		t.Fatal("next hop lost")
	}
}

func TestE7ProxyDelayAndCancellation(t *testing.T) {
	arrived := make(chan time.Time, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { arrived <- time.Now(); io.WriteString(w, "ok") }))
	defer upstream.Close()
	gate := newE7Proxy([]e7Target{{URL: upstream.URL, Site: 1}}, 40*time.Millisecond)
	proxy := httptest.NewServer(gate)
	defer proxy.Close()
	start := time.Now()
	r, e := http.Get(proxy.URL + "/0/0/check")
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	if a := <-arrived; a.Sub(start) < 15*time.Millisecond {
		t.Fatal("request half not delayed")
	}
	if time.Since(start) < 35*time.Millisecond {
		t.Fatal("response half not delayed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", proxy.URL+"/0/0/check", nil)
	if _, e = http.DefaultClient.Do(req); e == nil {
		t.Fatal("cancel ignored")
	}
	start = time.Now()
	r, e = http.Get(proxy.URL + "/1/0/check")
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	if a := <-arrived; a.Sub(start) >= 35*time.Millisecond {
		t.Fatal("same-site delayed")
	}
}
