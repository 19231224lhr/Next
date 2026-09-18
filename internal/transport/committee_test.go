package transport

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestSubmitPreservesDurableEnvelope(t *testing.T) {
	f := testkit.NewFixture("transport-retry", "a", 1)
	c, err := f.Certify(f.Transaction(0, 1))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := c.MarshalBinary()
	raw, _ := (protocol.Submission{Network: f.Org.Network, Body: body}).MarshalBinary()
	var received [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = append(received, b)
		w.WriteHeader(202)
	}))
	defer server.Close()
	client := NewCommitteeClient(server.URL)
	for i := 0; i < 2; i++ {
		if err := client.Submit(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
	}
	if len(received) != 2 || !bytes.Equal(received[0], raw) || !bytes.Equal(received[1], raw) {
		t.Fatal("retry changed durable envelope")
	}
}
