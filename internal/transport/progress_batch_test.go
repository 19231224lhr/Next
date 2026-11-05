package transport_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"utxo/internal/member"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/transport"
	"utxo/protocol"
)

func TestProgressBatchBoundsAndStatus(t *testing.T) {
	f := testkit.NewFixture("batch", "a", 1)
	db := store.NewMemory()
	defer db.Close()
	m, e := f.Member(0, db)
	if e != nil {
		t.Fatal(e)
	}
	h := transport.MemberHandler(m, 4, 4)
	facts := make([]protocol.SpendFactID, 128)
	raw, _ := json.Marshal(facts)
	for _, test := range []struct {
		body []byte
		ok   bool
	}{{raw, true}, {[]byte("[]"), false}, {append(raw, []byte("{}")...), false}, {[]byte("null"), false}, {bytes.Repeat([]byte(" "), 65537), false}} {
		req := httptest.NewRequest(http.MethodPost, "/v4/progress", bytes.NewReader(test.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if test.ok {
			var got []member.DirectStatus
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got) != 128 {
				t.Fatal(w.Code, w.Body.String())
			}
		} else if w.Code == 200 {
			t.Fatal("invalid accepted")
		}
	}
	raw, _ = json.Marshal(make([]protocol.SpendFactID, 129))
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v4/progress", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, req)
	if w.Code == 200 {
		t.Fatal("oversized batch accepted")
	}
}
