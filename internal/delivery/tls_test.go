package delivery

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"utxo/internal/store"
	"utxo/internal/wallet"
	"utxo/protocol"
)

func TestRecipientTLSClientTransport(t *testing.T) {
	f, p := fixture(t)
	owner := p.Tx.Body.Outputs[0].Recipient.Owner
	wd := store.NewMemory()
	defer wd.Close()
	w, e := wallet.NewWithGenesis(wd, f.Org.Network, owner, []protocol.OrgConfig{f.Org}, f.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	// Require a client certificate on a TLS wallet; the trusted test server
	// client provides hostname/root verification without InsecureSkipVerify.
	srv := httptest.NewUnstartedServer(Handler(map[protocol.PublicKey]*wallet.Wallet{owner: w}, nil))
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert, MinVersion: tls.VersionTLS13}
	srv.StartTLS()
	defer srv.Close()
	client := srv.Client()
	tr := client.Transport.(*http.Transport).Clone()
	tr.TLSClientConfig = tr.TLSClientConfig.Clone()
	tr.TLSClientConfig.Certificates = srv.TLS.Certificates
	client.Transport = tr
	db := store.NewMemory()
	defer db.Close()
	q, e := NewWithClient(db, []Route{{Owner: owner, URL: srv.URL}}, []protocol.OrgConfig{f.Org}, client)
	if e != nil {
		t.Fatal(e)
	}
	if e = q.Enqueue(p, ""); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.Run(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	delivered := false
	for time.Now().Before(deadline) {
		rows, e := store.Scan(db, []byte("pending/"), nil, 5)
		if e != nil {
			t.Fatal(e)
		}
		if len(rows) == 0 {
			delivered = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if !delivered {
		t.Fatal("TLS recipient not delivered")
	}
}
