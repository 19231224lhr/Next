package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"utxo/crypto/chameleon"
	"utxo/internal/rules"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestFaultGateBlocksBeforeForwardAndNeverReleasesBlockedRequest(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write([]byte("ok")) }))
	defer backend.Close()
	gate := newFaultGate([]string{backend.URL})
	control := httptest.NewRecorder()
	gate.ServeHTTP(control, httptest.NewRequest("POST", "/control", strings.NewReader(`{"Block":["0/v3/certificates"]}`)))
	if control.Code != 200 {
		t.Fatal(control.Code)
	}
	out := httptest.NewRecorder()
	gate.ServeHTTP(out, httptest.NewRequest("POST", "/0/v3/certificates", nil))
	if out.Code != 503 || calls.Load() != 0 {
		t.Fatal("gate forwarded blocked request")
	}
	gate.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/control", strings.NewReader(`{}`)))
	if calls.Load() != 0 {
		t.Fatal("unblocking released an old request")
	}
	out = httptest.NewRecorder()
	gate.ServeHTTP(out, httptest.NewRequest("POST", "/0/v3/certificates", nil))
	if out.Code != 200 || calls.Load() != 1 {
		t.Fatal("new request not forwarded")
	}
}

func TestFaultTamperPreservesPayerAuthButInvalidatesCertificate(t *testing.T) {
	f := testkit.NewFixture("e4-test", "org", 1)
	f.EnableDirect()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := chameleon.NewPublic(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	p := rules.DirectPolicy{Key: pub, Base: f.Schedule, TimeoutSeconds: 30, RepairCost: 5, Organizations: map[protocol.Hash]protocol.OrgConfig{f.Org.Hash(): f.Org}}
	tx, err := f.FastTransaction(0, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.DirectCertificate(tx, p)
	if err != nil {
		t.Fatal(err)
	}
	body := tx.Body
	body.Outputs = append([]protocol.Output(nil), body.Outputs...)
	body.Outputs[0].Amount++
	body.Intent = body.IntentID()
	changed, err := protocol.NewFastTx(body, tx.Claims, pub)
	if err != nil {
		t.Fatal(err)
	}
	changed.Auth = []protocol.OwnerAuth{protocol.SignOwner(changed.ID(), f.Owner)}
	raw, err := faultTamperedPayment(protocol.DirectPayment{Tx: tx, Certificate: c}, changed)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := protocol.DecodeDirectPayment(raw)
	if err != nil {
		t.Fatal("attack became a parse error", err)
	}
	if err = decoded.Tx.VerifyInitial(pub); err != nil {
		t.Fatal("payer authorization was broken", err)
	}
	if decoded.Tx.ID() != changed.ID() || decoded.Certificate.QC.Fact != c.QC.Fact {
		t.Fatal("wire mismatch")
	}
	if err = decoded.Certificate.Verify(f.Org); err == nil {
		t.Fatal("old certificate accepted modified transaction")
	}
}

func TestFaultGateDelayCancellation(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer backend.Close()
	gate := newFaultGate([]string{backend.URL})
	gate.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/control", strings.NewReader(`{"DelayMS":{"0/v3/transactions":1000}}`)))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	gate.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/0/v3/transactions", nil).WithContext(ctx))
	if time.Since(started) > 300*time.Millisecond {
		t.Fatal("delay ignored cancellation")
	}
}
