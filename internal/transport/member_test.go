package transport_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/transport"
	"utxo/protocol"
)

func TestMemberRejectsOversizedAndWrongMediaBeforeBusiness(t *testing.T) {
	f := testkit.NewFixture("http", "a", 1)
	db := store.NewMemory()
	defer db.Close()
	m, e := f.Member(0, db)
	if e != nil {
		t.Fatal(e)
	}
	handler := transport.MemberHandler(m, 4, 4)
	for _, test := range []struct {
		body    []byte
		content string
	}{{[]byte("{}"), "application/json"}, {bytes.Repeat([]byte{1}, protocol.MaxRequestBytes+1), transport.MediaType}} {
		req := httptest.NewRequest(http.MethodPost, "/v1/transactions", bytes.NewReader(test.body))
		req.Header.Set("Content-Type", test.content)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Fatal("invalid input accepted")
		}
	}
}
