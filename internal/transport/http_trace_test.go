package transport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"utxo/internal/requesttrace"
)

func TestMemberPostConnectionTrace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Header.Get(requesttrace.HeaderName) == "1" {
			ctx := requesttrace.Start(r.Context(), "member")
			requesttrace.Mark(ctx, "http_handler_enter")
			w.Header().Set(requesttrace.HeaderName, requesttrace.Header(ctx))
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	c := &MemberClient{BaseURL: server.URL, HTTP: NewHTTPClient(time.Second)}
	for _, connection := range []string{"http_got_conn_fresh", "http_got_conn_reused"} {
		ctx := requesttrace.Start(context.Background(), "gateway")
		body, err := c.post(ctx, "/v3/transactions", []byte("test"), 100)
		if err != nil || string(body) != "ok" {
			t.Fatalf("response: %q, %v", body, err)
		}
		seen := map[string]int64{}
		for _, event := range requesttrace.Events(ctx) {
			if event.Node == server.URL {
				seen[event.Stage] = event.UnixNS
			}
		}
		for _, stage := range []string{"http_do_start", "http_get_conn", connection,
			"http_request_written", "http_first_byte", "http_do_done", "http_body_read", "http_handler_enter"} {
			if seen[stage] == 0 {
				t.Fatalf("missing %s: %v", stage, seen)
			}
		}
		if seen["http_get_conn"] > seen[connection] || seen[connection] > seen["http_first_byte"] {
			t.Fatal("connection events reordered")
		}
	}
	off := context.Background()
	if _, err := c.post(off, "/v3/transactions", []byte("test"), 100); err != nil {
		t.Fatal(err)
	}
	if len(requesttrace.Events(off)) != 0 {
		t.Fatal("disabled tracing recorded events")
	}
}
