package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestObserveCommitWaitsForSettledState(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "DEFERRED", 404)
			return
		}
		if calls == 2 {
			w.Write([]byte(`{"PublicPhase":"REGISTERED"}`))
			return
		}
		w.Write([]byte(`{"PublicPhase":"SETTLED"}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := observeCommit(ctx, server.Client(), server.URL); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("accepted uncommitted state after %d queries", calls)
	}
}
