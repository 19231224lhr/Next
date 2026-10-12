package delivery

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/wallet"
	"utxo/protocol"
)

func fixture(t *testing.T) (testkit.Fixture, protocol.DirectPayment) {
	t.Helper()
	f := testkit.NewFixture("delivery", "org", 1)
	f.EnableDirect()
	raw, e := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := pem.Decode(raw)
	k, e := x509.ParsePKCS1PrivateKey(b.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	p, e := (rules.DirectSettings{Modulus: k.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}).Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	tx, e := f.FastTransaction(0, 1, p)
	if e != nil {
		t.Fatal(e)
	}
	c, e := f.DirectCertificate(tx, p)
	if e != nil {
		t.Fatal(e)
	}
	return f, protocol.DirectPayment{Tx: tx, Certificate: c}
}
func TestOfflineRestartAndDuplicate(t *testing.T) {
	f, p := fixture(t)
	owner := p.Tx.Body.Outputs[0].Recipient.Owner
	receiveDB, e := store.Open(filepath.Join(t.TempDir(), "wallet.db"), store.Identity{Network: "delivery", Role: "wallet", Node: "1", Schema: 1})
	if e != nil {
		t.Fatal(e)
	}
	defer receiveDB.Close()
	w, e := wallet.NewWithGenesis(receiveDB, f.Org.Network, owner, []protocol.OrgConfig{f.Org}, f.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	var online atomic.Bool
	var calls atomic.Int32
	h := Handler(map[protocol.PublicKey]*wallet.Wallet{owner: w}, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !online.Load() {
			http.Error(rw, "offline", 503)
			return
		}
		h.ServeHTTP(rw, r)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "delivery.db")
	id := store.Identity{Network: "delivery", Role: "delivery", Node: "1", Schema: 1}
	db, e := store.Open(path, id)
	if e != nil {
		t.Fatal(e)
	}
	q, e := New(db, []Route{{Owner: owner, URL: srv.URL}}, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	if e = q.Enqueue(p, ""); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if calls.Load() == 0 {
		t.Fatal("offline recipient not attempted")
	}
	db.Close()
	db, e = store.Open(path, id)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	q, e = New(db, []Route{{Owner: owner, URL: srv.URL}}, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	online.Store(true)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- q.Run(ctx) }()
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rows, _ := store.Scan(db, []byte("pending/"), nil, 10)
		if len(rows) == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	rows, e := store.Scan(db, []byte("pending/"), nil, 10)
	if e != nil || len(rows) != 0 {
		t.Fatalf("undelivered: %d %v", len(rows), e)
	}
	before := calls.Load()
	if e = q.Enqueue(p, ""); e != nil {
		t.Fatal(e)
	}
	time.Sleep(150 * time.Millisecond)
	if calls.Load() != before {
		t.Fatal("completed delivery reopened")
	}
	if e = receiveDB.View(func(v state.ReadView) error {
		_, e := v.Get(wallet.DirectCoinKey(p.Certificate.Summary.OutputID(0), 0))
		return e
	}); e != nil {
		t.Fatal(e)
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
func TestRecipientRejectsWrongOwnerAndBadQC(t *testing.T) {
	f, p := fixture(t)
	db := store.NewMemory()
	defer db.Close()
	owner := p.Tx.Body.Outputs[0].Recipient.Owner
	w, e := wallet.NewWithGenesis(db, f.Org.Network, owner, []protocol.OrgConfig{f.Org}, f.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"owner", "signature"} {
		t.Run(bad, func(t *testing.T) {
			pay := p
			wallets := map[protocol.PublicKey]*wallet.Wallet{owner: w}
			if bad == "owner" {
				wallets = map[protocol.PublicKey]*wallet.Wallet{}
			} else {
				pay.Certificate.QC.Votes = append([]protocol.SpendVote(nil), p.Certificate.QC.Votes...)
				pay.Certificate.QC.Votes[0].Signature[0] ^= 1
			}
			srv := httptest.NewServer(Handler(wallets, nil))
			defer srv.Close()
			raw, e := pay.MarshalBinary()
			if e != nil {
				t.Fatal(e)
			}
			if e = send(context.Background(), srv.Client(), srv.URL, record{Payment: raw, Index: 0}); e == nil {
				t.Fatal("invalid recipient accepted")
			}
		})
	}
}
func TestEnqueueDoesNotWaitForRecipientAndRejectsUnknownRoutes(t *testing.T) {
	f, p := fixture(t)
	db := store.NewMemory()
	defer db.Close()
	q, e := New(db, nil, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	if e = q.Enqueue(p, ""); e == nil {
		t.Fatal("unrouted payment silently accepted")
	}
	blocked := make(chan struct{})
	entered := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-blocked
	}))
	defer srv.Close()
	defer close(blocked)
	q, e = New(db, []Route{{Owner: p.Tx.Body.Outputs[0].Recipient.Owner, URL: srv.URL}}, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- q.Run(ctx) }()
	if e = q.Enqueue(p, ""); e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no delivery")
	}
	saved := make(chan error, 1)
	go func() { saved <- q.Enqueue(p, "") }()
	select {
	case e = <-saved:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("enqueue waited for recipient")
	}
	cancel()
	<-done
}

func TestInvalidCertificatesAndMetadataDoNotEnterOutbox(t *testing.T) {
	f, p := fixture(t)
	db := store.NewMemory()
	defer db.Close()
	q, e := New(db, []Route{{Owner: p.Tx.Body.Outputs[0].Recipient.Owner, URL: "http://127.0.0.1:1/receive"}}, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	if e = q.Enqueue(p, strings.Repeat("x", MaxMetadata+1)); e == nil {
		t.Fatal("oversized metadata accepted")
	}
	p.Certificate.QC.Votes[0].Signature[0] ^= 1
	if e = q.Enqueue(p, ""); e == nil {
		t.Fatal("bad QC accepted")
	}
	rows, e := store.Scan(db, []byte("pending/"), nil, 10)
	if e != nil || len(rows) != 0 {
		t.Fatal("invalid payment persisted")
	}
}

func TestDispatcherIsBoundedAndDoesNotFollowRedirects(t *testing.T) {
	f, p := fixture(t)
	db := store.NewMemory()
	defer db.Close()
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer other.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 307) }))
	defer first.Close()
	q, e := New(db, []Route{{Owner: p.Tx.Body.Outputs[0].Recipient.Owner, URL: first.URL}}, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := p.MarshalBinary()
	if e = send(context.Background(), q.client, first.URL, record{Payment: raw}); e == nil {
		t.Fatal("redirect acknowledged")
	}
	if redirected.Load() != 0 {
		t.Fatal("operator route escaped through redirect")
	}
	var active, peak atomic.Int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
		w.WriteHeader(503)
	}))
	defer slow.Close()
	defer close(release)
	q, e = New(db, []Route{{Owner: p.Tx.Body.Outputs[0].Recipient.Owner, URL: slow.URL}}, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 8; i++ {
		seed := protocol.Digest("bounded-recipient", []byte{byte(i)})
		key := ed25519.NewKeyFromSeed(seed[:])
		p.Tx.Body.Outputs[0].Recipient = protocol.NewDescriptor(f.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: f.Org.Org}, key)
		q.routes[p.Tx.Body.Outputs[0].Recipient.Owner] = slow.URL
		p.Tx.Body.Nonce[0] = byte(i + 1)
		p.Tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(p.Tx.ID(), f.Owner)}
		p.Certificate.Summary = protocol.SummaryFor(p.Tx, p.Certificate.Summary.Admission)
		p.Certificate.QC.Fact = p.Certificate.Summary.Fact()
		p.Certificate.QC.Votes = nil
		for j := 0; j < 3; j++ {
			p.Certificate.QC.Votes = append(p.Certificate.QC.Votes, protocol.SignSpend(p.Certificate.QC.Fact, uint16(j), f.Keys[j]))
		}
		if e = q.Enqueue(p, ""); e != nil {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.Run(ctx) }()
	for i := 0; i < workers; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("workers not dispatched")
		}
	}
	if peak.Load() != workers {
		t.Fatalf("workers=%d", peak.Load())
	}
	select {
	case <-entered:
		t.Fatal("unbounded dispatcher")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
