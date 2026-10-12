package delivery

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestSlowRecipientDoesNotHoldHealthyQueue(t *testing.T) {
	f, original := fixture(t)
	db := store.NewMemory()
	defer db.Close()
	healthy := make(chan struct{}, 8)
	var blockedCalls atomic.Int32
	release := make(chan struct{})
	blockedOwner := original.Tx.Body.Outputs[0].Recipient.Owner
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		p, e := protocol.DecodeDirectPayment(raw)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		if p.Tx.Body.Outputs[0].Recipient.Owner == blockedOwner {
			blockedCalls.Add(1)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		} else {
			healthy <- struct{}{}
		}
		fmt.Fprint(w, protocol.Hash(p.Certificate.Summary.OutputID(0)).String())
	}))
	defer srv.Close()
	defer close(release)
	var routes []Route
	for i := 0; i < 9; i++ {
		p := original
		p.Tx.Body.Outputs = append([]protocol.Output(nil), original.Tx.Body.Outputs...)
		p.Tx.Body.Nonce[0] = byte(i + 1)
		if i > 1 {
			seed := protocol.Digest("delivery-scheduler-owner", []byte{byte(i)})
			key := ed25519.NewKeyFromSeed(seed[:])
			p.Tx.Body.Outputs[0].Recipient = protocol.NewDescriptor(f.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: f.Org.Org}, key)
		}
		owner := p.Tx.Body.Outputs[0].Recipient.Owner
		if i != 1 {
			routes = append(routes, Route{Owner: owner, URL: srv.URL})
		}
		p.Tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(p.Tx.ID(), f.Owner)}
		p.Certificate.Summary = protocol.SummaryFor(p.Tx, original.Certificate.Summary.Admission)
		p.Certificate.QC.Fact = p.Certificate.Summary.Fact()
		p.Certificate.QC.Votes = nil
		for j := 0; j < 3; j++ {
			p.Certificate.QC.Votes = append(p.Certificate.QC.Votes, protocol.SignSpend(p.Certificate.QC.Fact, uint16(j), f.Keys[j]))
		}
		raw, e := p.MarshalBinary()
		if e != nil {
			t.Fatal(e)
		}
		value, e := json.Marshal(record{Payment: raw})
		if e != nil {
			t.Fatal(e)
		}
		// Ordered keys isolate scheduler behavior; the first two jobs share an offline recipient.
		e = db.Update(func(state.ReadView) ([]state.Change, error) {
			return []state.Change{{Key: []byte(fmt.Sprintf("pending/%04d", i)), Value: value}}, nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	q, e := New(db, routes, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.Run(ctx) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	start := time.Now()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for i := 0; i < 7; i++ {
		select {
		case <-healthy:
		case <-deadline.C:
			t.Fatalf("only %d healthy deliveries escaped blocked recipient", i)
		}
	}
	if n := blockedCalls.Load(); n != 1 {
		t.Fatalf("same recipient has %d concurrent requests, want 1", n)
	}
	t.Logf("seven healthy deliveries completed in %s while blocked recipient remained pending", time.Since(start))
}

func TestRejectedRecordDoesNotStarveSameRecipient(t *testing.T) {
	f, p := fixture(t)
	db := store.NewMemory()
	defer db.Close()
	got := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(MetadataHeader) == "reject" {
			http.Error(w, "reject this record", 400)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		payment, e := protocol.DecodeDirectPayment(raw)
		if e != nil {
			t.Error(e)
			return
		}
		fmt.Fprint(w, protocol.Hash(payment.Certificate.Summary.OutputID(0)).String())
		select {
		case got <- struct{}{}:
		default:
		}
	}))
	defer srv.Close()
	q, e := New(db, []Route{{Owner: p.Tx.Body.Outputs[0].Recipient.Owner, URL: srv.URL}}, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	raw, e := p.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	for i, metadata := range []string{"reject", "reject", "accept"} {
		value, _ := json.Marshal(record{Payment: raw, Metadata: metadata})
		e = db.Update(func(state.ReadView) ([]state.Change, error) {
			return []state.Change{{Key: []byte(fmt.Sprintf("pending/%04d", i)), Value: value}}, nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.Run(ctx) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("later valid record starved by rejected record for the same recipient")
	}
}
