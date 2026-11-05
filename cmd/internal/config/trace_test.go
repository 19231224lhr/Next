package config

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoopbackRuntimeTrace(t *testing.T) {
	s, err := HTTP("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("application"))
	}), TLS{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s.Handler)
	defer server.Close()
	response, err := http.Get(server.URL + "/debug/pprof/trace?seconds=0.02")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || !bytes.HasPrefix(body, []byte("go 1.")) || len(body) < 32 {
		t.Fatalf("not a runtime trace: %d bytes, %v", len(body), err)
	}
}
