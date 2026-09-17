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
	// Durable collector replay needs no live members and creates no new identity.
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
