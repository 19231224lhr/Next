package sameorglab_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"utxo/internal/transport"
	"utxo/protocol"
)

func TestCachedParentStillRejectsForgeryAndDoubleSpend(t *testing.T) {
	l := newLab(t, variants[4])
	parent := l.request(t, 0, nil)
	cert, err := l.collector.CollectDirect(context.Background(), parent)
	must(t, err)
	p := protocol.DirectPayment{Tx: parent.Tx, Certificate: cert}
	l.install(t, p)
	child := l.request(t, 1, &p)
	_, err = l.clients[0].ApproveDirect(context.Background(), child)
	must(t, err)
	before := l.quota(t, protocol.ResourceCAL)
	bad := child
	bad.InputCertificates = slices.Clone(child.InputCertificates)
	bad.InputCertificates[0].Certificate.QC.Votes = slices.Clone(cert.QC.Votes)
	bad.InputCertificates[0].Certificate.QC.Votes[0].Signature[0] ^= 1
	if _, err = l.clients[0].ApproveDirect(context.Background(), bad); err == nil {
		t.Fatal("forgery accepted after cache warmup")
	}
	_, err = l.clients[0].ApproveDirect(context.Background(), child)
	must(t, err)
	body := child.Tx.Body
	body.Nonce[0] ^= 0x80
	conflict, err := protocol.NewFastTx(body, child.Tx.Claims, l.policy.Key)
	must(t, err)
	conflict.Auth = []protocol.OwnerAuth{protocol.SignOwner(conflict.ID(), l.keys[1])}
	if _, err = l.clients[0].ApproveDirect(context.Background(), protocol.DirectRequest{Tx: conflict, InputCertificates: child.InputCertificates}); err == nil {
		t.Fatal("cached parent bypassed spend lock")
	}
	if l.quota(t, protocol.ResourceCAL) != before {
		t.Fatal("retry or rejected request changed capacity")
	}
}

func TestReferenceHTTPFallbackOnlyOnMissOrUnsupported(t *testing.T) {
	l := newLab(t, variants[0])
	parent := l.request(t, 0, nil)
	cert, err := l.f.DirectCertificate(parent.Tx, l.policy)
	must(t, err)
	child := l.request(t, 1, &protocol.DirectPayment{Tx: parent.Tx, Certificate: cert})
	for _, tc := range []struct {
		name     string
		status   int
		code     string
		fallback bool
	}{{"miss", 428, "CERTIFICATE_CACHE_MISS", true}, {"old-peer", 404, "not found", true}, {"auth", 400, "INVALID_AUTH", false}, {"busy", 429, "LIMITED", false}} {
		t.Run(tc.name, func(t *testing.T) {
			var count atomic.Int64
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				if strings.HasSuffix(r.URL.Path, "/references") {
					http.Error(w, tc.code, tc.status)
					return
				}
				_ = json.NewEncoder(w).Encode(protocol.DirectApproval{Vote: protocol.SpendVote{Member: 2}})
			}))
			defer s.Close()
			client := transport.NewMemberClient(s.URL)
			client.PreferDirectReferences = true
			a, err := client.ApproveDirect(context.Background(), child)
			if tc.fallback {
				if err != nil || a.Vote.Member != 2 || count.Load() != 2 {
					t.Fatal(a, err, count.Load())
				}
			} else if err == nil || count.Load() != 1 {
				t.Fatal("unexpected retry", err, count.Load())
			}
		})
	}
}
