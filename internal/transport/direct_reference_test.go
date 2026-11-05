package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"utxo/protocol"
)

func TestMemberHTTPErrorAndApprovalDecoding(t *testing.T) {
	want := protocol.DirectApproval{Vote: protocol.SpendVote{Member: 2}}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.Error(w, "CERTIFICATE_CACHE_MISS", 428)
			return
		}
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer s.Close()
	c := NewMemberClient(s.URL)
	if _, err := c.post(context.Background(), "/missing", nil, 1024); err == nil {
		t.Fatal("miss accepted")
	}
	a, err := c.ApproveDirectBytes(context.Background(), []byte{1})
	if err != nil || a.Vote.Member != 2 {
		t.Fatal(a, err)
	}
}
