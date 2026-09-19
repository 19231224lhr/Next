package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"utxo/internal/gateway"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/transport"
	"utxo/protocol"
)

type blockedStore struct {
	store.Store
	entered, release chan struct{}
}

func (s *blockedStore) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	close(s.entered)
	<-s.release
	return s.Store.Update(fn)
}

func TestFullHTTPResponseArrivesBeforeGatewayCommit(t *testing.T) {
	f := testkit.NewFixture("flush-before-commit", "a", 1)
	var clients [4]gateway.MemberClient
	for i := range clients {
		db := store.NewMemory()
		defer db.Close()
		m, e := f.Member(i, db)
		if e != nil {
			t.Fatal(e)
		}
		srv := httptest.NewServer(transport.MemberHandler(m, 4, 4))
		defer srv.Close()
		clients[i] = transport.NewMemberClient(srv.URL)
	}
	db := &blockedStore{Store: store.NewMemory(), entered: make(chan struct{}), release: make(chan struct{})}
	group, e := store.NewGroup(db, 256, 64)
	if e != nil {
		t.Fatal(e)
	}
	defer group.Close()
	collector, e := gateway.New(f.Org, clients, group)
	if e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer(paymentHandler(collector))
	defer srv.Close()
	defer close(db.release) // Unblock the handler before Server.Close waits for it.
	raw, e := (protocol.PaymentRequest{Tx: f.Transaction(0, 1)}).MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, bytes.NewReader(raw))
		if err != nil {
			result <- err
			return
		}
		req.Header.Set("Content-Type", transport.MediaType)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			result <- err
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			result <- err
			return
		}
		cert, err := protocol.DecodeCertificate(body)
		if err == nil {
			err = cert.Verify(f.Org)
		}
		result <- err
	}()
	select {
	case <-db.entered:
	case <-ctx.Done():
		t.Fatal("background persistence did not start")
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("client body read waited for the blocked gateway commit")
	}
}
