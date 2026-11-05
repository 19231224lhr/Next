package transport

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInstallDirectBytesPostsExactBody(t *testing.T) {
	expected := []byte("immutable verified payment")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v3/certificates" || !bytes.Equal(raw, expected) {
			t.Error("wrong install request")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := NewMemberClient(server.URL)
	if err := client.InstallDirectBytes(context.Background(), expected); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.InstallDirectBytes(ctx, expected); err == nil {
		t.Fatal("cancellation ignored")
	}
}
