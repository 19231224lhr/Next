package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"utxo/protocol"
)

func TestPreparedApprovalFallbackIsExplicit(t *testing.T) {
	full, compact := []byte{0, 1}, []byte{0, 2}
	want := protocol.DirectApproval{Vote: protocol.SpendVote{Member: 2}}
	encoded, e := want.MarshalCompact()
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name, code    string
		status, calls int
		success       bool
	}{
		{"missing", "PARENT_APPROVAL_MISSING", 428, 2, true},
		{"explicit-unsupported", "UNSUPPORTED", 422, 2, true},
		{"old-route", "NOT_FOUND", 404, 2, true},
		{"auth", "INVALID_AUTH", 400, 1, false},
		{"unknown-400", "INVALID_REQUEST", 400, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				if calls == 1 {
					http.Error(w, tc.code, tc.status)
					return
				}
				if !bytes.Equal(raw, full) {
					t.Error("fallback did not send full request")
				}
				if r.URL.Path == "/v3/transactions" {
					json.NewEncoder(w).Encode(want)
					return
				}
				binaryResponse(w, encoded)
			}))
			defer srv.Close()
			got, e := NewMemberClient(srv.URL).ApproveDirectPrepared(context.Background(), protocol.FastTx{}, full, compact)
			if (e == nil) != tc.success || calls != tc.calls {
				t.Fatalf("error=%v calls=%d", e, calls)
			}
			if tc.success && got.Vote != want.Vote {
				t.Fatal("changed vote")
			}
		})
	}
}
