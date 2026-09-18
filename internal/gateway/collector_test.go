package gateway_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
	"utxo/internal/gateway"
	"utxo/internal/member"
	"utxo/internal/requesttrace"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/transport"
	"utxo/protocol"
)

type unavailable struct{}

func (unavailable) Approve(context.Context, protocol.PaymentRequest) (protocol.Approval, error) {
	return protocol.Approval{}, errors.New("offline")
}
func (unavailable) Install(context.Context, protocol.TXCer) error { return errors.New("offline") }
func TestHTTPCollectorOneOfflineAndChildWithoutInstall(t *testing.T) {
	f := testkit.NewFixture("http", "a", 1)
	var clients [4]gateway.MemberClient
	var members [3]*member.Member
	for i := 0; i < 3; i++ {
		db, e := store.Open(filepath.Join(t.TempDir(), "member.db"), store.Identity{Network: f.Org.Network.String(), Role: "member", Node: string(rune('0' + i)), Schema: 2})
		if e != nil {
			t.Fatal(e)
		}
		group, e := store.NewGroup(db, 64, 16)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { group.Close() })
		members[i], e = f.Member(i, group)
		if e != nil {
			t.Fatal(e)
		}
		server := httptest.NewServer(transport.MemberHandler(members[i], 32, 8))
		t.Cleanup(server.Close)
		clients[i] = transport.NewMemberClient(server.URL)
	}
	clients[3] = unavailable{}
	db := store.NewMemory()
	defer db.Close()
	collector, e := gateway.New(f.Org, clients, db)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	certificate, e := collector.Collect(ctx, protocol.PaymentRequest{Tx: f.Transaction(0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	if e = certificate.Verify(f.Org); e != nil {
		t.Fatal(e)
	}
	// Foreground collection did not install on any member.
	for _, m := range members {
		if _, e = m.Certificate(certificate.QC.Fact); !errors.Is(e, state.ErrNotFound) {
			t.Fatalf("unexpected install: %v", e)
		}
	}
	body := f.Transaction(0, 2).Body
	body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: certificate.Effects.Outputs[0], Evidence: protocol.Hash(certificate.QC.Fact)}}
	child, e := collector.Collect(ctx, protocol.PaymentRequest{Tx: f.Sign(body), Parents: []protocol.TXCer{certificate}})
	if e != nil {
		t.Fatal(e)
	}
	if e = child.Verify(f.Org); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		if e = clients[i].Install(ctx, certificate); e != nil {
			t.Fatal(e)
		}
	}
	// Background persistence makes replay independent of live members.
	if e = collector.Persist(certificate); e != nil {
		t.Fatal(e)
	}
	for i := range clients {
		clients[i] = unavailable{}
	}
	restarted, e := gateway.New(f.Org, clients, db)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := restarted.Collect(ctx, protocol.PaymentRequest{Tx: f.Transaction(0, 1)})
	if e != nil || replay.QC.Fact != certificate.QC.Fact {
		t.Fatalf("replay %v", e)
	}
}

func TestDiagnosticTraceIncludesThreeMembersWithoutWaitingForFourth(t *testing.T) {
	f := testkit.NewFixture("trace", "a", 1)
	var clients [4]gateway.MemberClient
	for i := 0; i < 3; i++ {
		db := store.NewMemory()
		defer db.Close()
		m, e := f.Member(i, db)
		if e != nil {
			t.Fatal(e)
		}
		server := httptest.NewServer(transport.MemberHandler(m, 4, 4))
		defer server.Close()
		clients[i] = transport.NewMemberClient(server.URL)
	}
	clients[3] = unavailable{}
	db := store.NewMemory()
	defer db.Close()
	collector, e := gateway.New(f.Org, clients, db)
	if e != nil {
		t.Fatal(e)
	}
	ctx := requesttrace.Start(context.Background(), "gateway")
	cert, e := collector.Collect(ctx, protocol.PaymentRequest{Tx: f.Transaction(0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	if e = cert.Verify(f.Org); e != nil {
		t.Fatal(e)
	}
	stages := make(map[string]int)
	for _, event := range requesttrace.Events(ctx) {
		stages[event.Stage]++
	}
	for stage, want := range map[string]int{"http_handler_enter": 3, "validation_complete": 3, "state_checks_complete": 3, "commit_returned": 3, "vote_signed": 3, "quorum_collected": 1, "certificate_verified": 1} {
		if stages[stage] != want {
			t.Fatalf("%s count=%d want %d", stage, stages[stage], want)
		}
	}
}

type failedWriter struct {
	store.Store
	writes int
}

func (s *failedWriter) Update(func(state.ReadView) ([]state.Change, error)) error {
	s.writes++
	return errors.New("disk unavailable")
}
func TestCollectDoesNotWaitForOrRequireGatewayPersistence(t *testing.T) {
	f := testkit.NewFixture("no-gateway-write", "a", 1)
	var clients [4]gateway.MemberClient
	for i := range clients {
		db := store.NewMemory()
		defer db.Close()
		m, e := f.Member(i, db)
		if e != nil {
			t.Fatal(e)
		}
		server := httptest.NewServer(transport.MemberHandler(m, 4, 4))
		defer server.Close()
		clients[i] = transport.NewMemberClient(server.URL)
	}
	db := &failedWriter{Store: store.NewMemory()}
	defer db.Close()
	collector, e := gateway.New(f.Org, clients, db)
	if e != nil {
		t.Fatal(e)
	}
	cert, e := collector.Collect(context.Background(), protocol.PaymentRequest{Tx: f.Transaction(0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	if e = cert.Verify(f.Org); e != nil {
		t.Fatal(e)
	}
	if db.writes != 0 {
		t.Fatal("foreground performed a storage write")
	}
	if e = collector.Persist(cert); e == nil {
		t.Fatal("background persistence failure hidden")
	}
}
