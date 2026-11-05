package transport

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestPendingReceiptReusesHTTPConnection(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "PROOF_PENDING", http.StatusServiceUnavailable)
	}))
	server.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	client := NewCommitteeClient(server.URL)
	client.HTTP = server.Client()
	defer client.HTTP.CloseIdleConnections()
	for i := 0; i < 20; i++ {
		if _, err := client.get(context.Background(), "/pending", 4096); err == nil {
			t.Fatal("pending receipt accepted")
		}
	}
	if n := connections.Load(); n != 1 {
		t.Fatalf("20 pending polls opened %d connections; error body was not drained", n)
	}
}

func TestOversizedErrorBodyRemainsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = io.WriteString(w, string(make([]byte, 8192)))
	}))
	defer server.Close()
	if _, err := NewCommitteeClient(server.URL).get(context.Background(), "/pending", 4096); err == nil {
		t.Fatal("error accepted")
	}
}
