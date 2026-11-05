package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"utxo/protocol"
)

type routedRequest struct {
	endpoint int
	body     []byte
}

func routingEnvelope(t *testing.T, n int) []byte {
	t.Helper()
	s := protocol.Submission{Network: protocol.Hash{1}, Body: []byte(fmt.Sprint("fixture-", n))}
	s.Nonce[0], s.Nonce[1] = byte(n), byte(n>>8)
	raw, err := s.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCommitteeRoutingStableDistributionAndBaseQuery(t *testing.T) {
	requests := make(chan routedRequest, 128)
	urls := make([]string, 4)
	for i := range urls {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				fmt.Fprint(w, i)
				return
			}
			if r.URL.Path != "/v1/commands" || r.Header.Get("Content-Type") != MediaType {
				t.Error("submission request changed")
			}
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			requests <- routedRequest{i, raw}
			w.WriteHeader(http.StatusAccepted)
		}))
		t.Cleanup(server.Close)
		urls[i] = server.URL
	}
	// A repeated BaseURL must not bias routing or cause duplicate fallback.
	client := NewCommitteeClient(urls[0]+"/", urls[1], urls[2], urls[3], urls[0])
	t.Cleanup(client.HTTP.CloseIdleConnections)
	counts := make([]int, 4)
	for i := 0; i < 64; i++ {
		raw := routingEnvelope(t, i)
		for j := 0; j < 2; j++ {
			if err := client.Submit(context.Background(), raw); err != nil {
				t.Fatal(err)
			}
		}
		a, b := <-requests, <-requests
		if a.endpoint != b.endpoint || !bytes.Equal(a.body, raw) || !bytes.Equal(b.body, raw) {
			t.Fatal("same envelope changed destination or bytes")
		}
		counts[a.endpoint]++
		if len(requests) != 0 {
			t.Fatal("successful submit broadcast to additional endpoints")
		}
	}
	for i, n := range counts {
		if n == 0 {
			t.Fatalf("endpoint %d received no requests: %v", i, counts)
		}
	}
	query, err := client.get(context.Background(), "/query", 64)
	if err != nil || string(query) != "0" {
		t.Fatalf("query moved away from BaseURL: %q %v", query, err)
	}
}

func TestCommitteeRoutingBoundedFallbackAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		status                          int
		network, cancel, preCancel, all bool
		calls                           int
		success                         bool
	}{
		{name: "503", status: 503, calls: 2, success: true},
		{name: "429", status: 429, calls: 2, success: true},
		{name: "accepted-response-lost", network: true, calls: 2, success: true},
		{name: "400", status: 400, calls: 1},
		{name: "all503", status: 503, all: true, calls: 4},
		{name: "cancel-after-first", status: 503, cancel: true, calls: 1},
		{name: "already-cancelled", preCancel: true, calls: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var enabled atomic.Bool
			var preferred atomic.Int32
			requests := make(chan routedRequest, 16)
			urls := make([]string, 4)
			for i := range urls {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					raw, _ := io.ReadAll(r.Body)
					requests <- routedRequest{i, raw}
					if enabled.Load() && (tc.all || int32(i) == preferred.Load()) {
						if tc.cancel {
							cancel()
						}
						if tc.network {
							// The server received the complete command, but its reply is lost.
							// Fallback must preserve both the envelope and its nonce.
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
								return
							}
							conn.Close()
							return
						}
						w.WriteHeader(tc.status)
						return
					}
					w.WriteHeader(http.StatusAccepted)
				}))
				t.Cleanup(server.Close)
				urls[i] = server.URL
			}
			client := NewCommitteeClient(urls[0], urls[1:]...)
			t.Cleanup(client.HTTP.CloseIdleConnections)
			raw := routingEnvelope(t, 7)
			if err := client.Submit(context.Background(), raw); err != nil {
				t.Fatal(err)
			}
			first := <-requests
			preferred.Store(int32(first.endpoint))
			enabled.Store(true)
			if tc.preCancel {
				cancel()
			}
			err := client.Submit(ctx, raw)
			if (err == nil) != tc.success {
				t.Fatalf("error=%v success=%v", err, tc.success)
			}
			if (tc.cancel || tc.preCancel) && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if len(requests) != tc.calls {
				t.Fatalf("requests=%d want=%d", len(requests), tc.calls)
			}
			seen := map[int]bool{}
			for i := 0; i < tc.calls; i++ {
				r := <-requests
				if i == 0 && r.endpoint != first.endpoint {
					t.Fatal("first choice changed")
				}
				if seen[r.endpoint] || !bytes.Equal(r.body, raw) {
					t.Fatal("retried an endpoint or changed envelope/nonce")
				}
				seen[r.endpoint] = true
			}
		})
	}
}

func TestCommitteeRoutingBlackholeLeavesTimeForFallback(t *testing.T) {
	for _, parentDeadline := range []bool{true, false} {
		for _, stallBody := range []bool{false, true} {
			t.Run(fmt.Sprintf("parent-deadline=%v/body=%v", parentDeadline, stallBody), func(t *testing.T) {
				var enabled atomic.Bool
				var preferred atomic.Int32
				requests := make(chan routedRequest, 8)
				urls := make([]string, 4)
				for i := range urls {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						raw, _ := io.ReadAll(r.Body)
						requests <- routedRequest{i, raw}
						if enabled.Load() && int32(i) == preferred.Load() {
							if stallBody {
								w.WriteHeader(http.StatusServiceUnavailable)
								w.(http.Flusher).Flush()
								// A reply can also stall after the headers arrive.
							}
							<-r.Context().Done()
							return
						}
						w.WriteHeader(http.StatusAccepted)
					}))
					t.Cleanup(server.Close)
					urls[i] = server.URL
				}
				client := NewCommitteeClient(urls[0], urls[1:]...)
				t.Cleanup(client.HTTP.CloseIdleConnections)
				raw := routingEnvelope(t, 33)
				if err := client.Submit(context.Background(), raw); err != nil {
					t.Fatal(err)
				}
				first := <-requests
				preferred.Store(int32(first.endpoint))
				enabled.Store(true)
				const budget = 800 * time.Millisecond
				client.HTTP.Timeout = budget
				ctx := context.Background()
				if parentDeadline {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, budget)
					defer cancel()
				}
				started := time.Now()
				err := client.Submit(ctx, raw)
				if err != nil || time.Since(started) >= budget {
					t.Fatalf("fallback exceeded total budget: elapsed=%v err=%v", time.Since(started), err)
				}
				if len(requests) != 2 {
					t.Fatalf("requests=%d want=2", len(requests))
				}
				a, b := <-requests, <-requests
				if a.endpoint != first.endpoint || b.endpoint == first.endpoint || !bytes.Equal(a.body, raw) || !bytes.Equal(b.body, raw) {
					t.Fatal("fallback changed first choice or envelope/nonce")
				}
			})
		}
	}
}

func TestCommitteeRoutingSingleURLAndLiteralRemainSingle(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer server.Close()
	for _, client := range []*CommitteeClient{NewCommitteeClient(server.URL), {BaseURL: server.URL, HTTP: server.Client()}} {
		before := calls.Load()
		if err := client.Submit(context.Background(), routingEnvelope(t, 1)); err == nil {
			t.Fatal("503 accepted")
		}
		if calls.Load() != before+1 {
			t.Fatal("single URL client unexpectedly retried")
		}
		client.HTTP.CloseIdleConnections()
	}
}
