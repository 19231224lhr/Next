package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeliveryEnqueueFailureRetriesIdenticalRequest(t *testing.T) {
	raw := []byte("already signed request")
	n := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		b, _ := io.ReadAll(r.Body)
		if !bytes.Equal(b, raw) {
			t.Error("changed signed bytes")
		}
		if n == 1 {
			http.Error(w, "DELIVERY_PERSIST_FAILED_RETRY_SAME_REQUEST", 503)
			return
		}
		w.Write([]byte("certificate"))
	}))
	defer s.Close()
	b, _, attempts, e := submitChain(context.Background(), s.Client(), s.URL, raw, false)
	if e != nil || attempts != 2 || string(b) != "certificate" {
		t.Fatalf("%s attempts=%d err=%v", b, attempts, e)
	}
}
