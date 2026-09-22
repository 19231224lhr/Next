package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"utxo/internal/gateway"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/transport"
	"utxo/protocol"
)

type readyDirectMember struct {
	gateway.MemberClient
	approval protocol.DirectApproval
}

func (m readyDirectMember) ApproveDirect(context.Context, protocol.DirectRequest) (protocol.DirectApproval, error) {
	return m.approval, nil
}
func (m readyDirectMember) InstallDirect(context.Context, protocol.DirectPayment) error { return nil }

func directHTTPFixture(t *testing.T, db store.Store) (*gateway.Collector, protocol.DirectRequest, protocol.OrgConfig) {
	t.Helper()
	f := testkit.NewFixture("early-http", "org", 1)
	f.EnableDirect()
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := (rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}).Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.FastTransaction(0, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := f.DirectCertificate(tx, policy)
	if err != nil {
		t.Fatal(err)
	}
	var clients [4]gateway.MemberClient
	for i := range clients {
		clients[i] = readyDirectMember{approval: protocol.DirectApproval{Summary: cert.Summary, Vote: protocol.SignSpend(cert.QC.Fact, uint16(i), f.Keys[i])}}
	}
	c, err := gateway.New(f.Org, clients, db)
	if err != nil {
		t.Fatal(err)
	}
	return c, protocol.DirectRequest{Tx: tx}, f.Org
}

func TestCollectOnlyReturnsCertificateWithoutPublicDelivery(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	c, request, org := directHTTPFixture(t, db)
	c.EnableSerialDirect()
	c.OfferDirect = func(protocol.DirectPayment) bool { t.Error("withheld parent offered for delivery"); return true }
	handler, drain := newDirectPaymentHandler(c, false)
	defer drain()
	raw, err := request.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/debug/budget/collect", bytes.NewReader(raw))
	r.Header.Set("Content-Type", transport.MediaType)
	w := httptest.NewRecorder()
	handler(w, r)
	cert, err := protocol.DecodeOutputCertificate(w.Body.Bytes())
	if w.Code != 200 || err != nil || cert.Verify(org) != nil {
		t.Fatalf("certificate: %d %v", w.Code, err)
	}
	rows, err := store.Scan(db, state.Key(state.KeyOutbox), nil, 1)
	if err != nil || len(rows) != 0 {
		t.Fatalf("collect-only created outbox: %v", err)
	}
	// Retrying a lost response uses the saved result and still withholds delivery.
	r = httptest.NewRequest("POST", "/debug/budget/collect", bytes.NewReader(raw))
	r.Header.Set("Content-Type", transport.MediaType)
	w = httptest.NewRecorder()
	handler(w, r)
	if w.Code != 200 {
		t.Fatalf("retry: %d", w.Code)
	}
	rows, err = store.Scan(db, state.Key(state.KeyOutbox), nil, 1)
	if err != nil || len(rows) != 0 {
		t.Fatal("retry published withheld parent")
	}
}

func TestDirectHTTPResponseAndEarlyOfferBeforePersistence(t *testing.T) {
	db := &blockedStore{Store: store.NewMemory(), entered: make(chan struct{}), release: make(chan struct{})}
	defer db.Close()
	c, request, org := directHTTPFixture(t, db)
	tx := request.Tx
	offered := make(chan protocol.DirectPayment, 2)
	c.OfferDirect = func(p protocol.DirectPayment) bool { offered <- p; return true }
	handler, drain := directPaymentHandler(c)
	defer drain()
	srv := httptest.NewServer(handler)
	defer srv.Close()
	defer close(db.release)
	raw, err := request.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{MaxConnsPerHost: 1}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 2 * time.Second}
	resp, err := client.Post(srv.URL, transport.MediaType, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	got, err := protocol.DecodeOutputCertificate(body)
	if err != nil || got.Verify(org) != nil {
		t.Fatal("invalid response", err)
	}
	select {
	case p := <-offered:
		if p.Tx.ID() != tx.ID() || p.Certificate.Verify(org) != nil {
			t.Fatal("wrong early payload")
		}
	case <-time.After(time.Second):
		t.Fatal("early offer waited for persistence")
	}
	select {
	case <-db.entered:
	case <-time.After(time.Second):
		t.Fatal("durable save was skipped")
	}
	// Reuse exactly the connection whose first handler is still saving.
	reused := make(chan bool, 1)
	req, err := http.NewRequest("POST", srv.URL, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", transport.MediaType)
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { reused <- info.Reused },
	}))
	second, err := client.Do(req)
	if err != nil {
		t.Fatal("second payment waited for preceding handler persistence:", err)
	}
	body, err = io.ReadAll(second.Body)
	second.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !<-reused {
		t.Fatal("second request did not reuse the original connection")
	}
	got, err = protocol.DecodeOutputCertificate(body)
	if err != nil || got.Verify(org) != nil {
		t.Fatal("invalid second response", err)
	}
}

func TestDirectPersistenceBoundAndDrain(t *testing.T) {
	db := &blockedStore{Store: store.NewMemory(), entered: make(chan struct{}), release: make(chan struct{})}
	c, request, _ := directHTTPFixture(t, db)
	raw, err := request.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	handler, drain := directPaymentHandler(c)
	returned := make(chan context.Context, directPersistenceLimit+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r)
		returned <- r.Context()
	}))
	release := sync.OnceFunc(func() { close(db.release) })
	defer func() { release(); srv.Close(); drain(); db.Close() }()
	tr := &http.Transport{MaxConnsPerHost: 1}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	send := func() {
		t.Helper()
		resp, err := client.Post(srv.URL, transport.MediaType, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("response %d: %v", resp.StatusCode, err)
		}
	}
	for i := 0; i < directPersistenceLimit; i++ {
		send()
		select {
		case ctx := <-returned:
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("handler context still active")
			}
		case <-time.After(time.Second):
			t.Fatal("handler waited for background save")
		}
	}
	// The next save must stay in the handler, not spawn an unbounded extra task.
	send()
	deadline := time.Now().Add(time.Second)
	for db.updates.Load() != directPersistenceLimit+1 {
		if time.Now().After(deadline) {
			t.Fatal("full capacity dropped a save")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-returned:
		t.Fatal("full capacity did not use synchronous fallback")
	default:
	}
	drained := make(chan struct{})
	go func() { drain(); close(drained) }()
	select {
	case <-drained:
		t.Fatal("drain returned before saves finished")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("drain did not complete")
	}
	// Saving outlives all request contexts; the durable record is still written.
	if err := db.View(func(v state.ReadView) error {
		id := request.Tx.ID()
		_, err := v.Get(state.Key(state.KeyCollected, id[:]))
		return err
	}); err != nil {
		t.Fatal("accepted save missing after drain:", err)
	}
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest("POST", "/v3/transactions", bytes.NewReader(raw)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal("drained handler accepted another request")
	}
}

type failingDirectStore struct{ store.Store }

func (failingDirectStore) Update(func(state.ReadView) ([]state.Change, error)) error {
	return io.ErrClosedPipe
}

func TestDirectPersistenceFailureIsLoggedBeforeDrainReturns(t *testing.T) {
	db := failingDirectStore{store.NewMemory()}
	defer db.Close()
	c, request, _ := directHTTPFixture(t, db)
	raw, err := request.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	handler, drain := directPaymentHandler(c)
	defer drain()
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", "/v3/transactions", bytes.NewReader(raw))
		req.Header.Set("Content-Type", transport.MediaType)
		w := httptest.NewRecorder()
		handler(w, req)
		if w.Code != http.StatusOK {
			t.Fatal("persistence failure changed the already returned certificate")
		}
	}
	drain()
	if strings.Count(logs.String(), "background v3 certificate persistence failed") != 3 ||
		!strings.Contains(logs.String(), "spend=") || !strings.Contains(logs.String(), io.ErrClosedPipe.Error()) {
		t.Fatal("background persistence errors were lost:", logs.String())
	}
}
