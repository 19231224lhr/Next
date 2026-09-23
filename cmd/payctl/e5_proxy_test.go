package main

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/rules"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestE5ProxyReadsUpstreamBeforeWaitingAndIgnoresObservedNoop(t *testing.T) {
	f := testkit.NewFixture("e5-proxy", "org", 1)
	f.EnableDirect()
	raw, e := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if e != nil {
		t.Fatal(e)
	}
	block, _ := pem.Decode(raw)
	key, e := x509.ParsePKCS1PrivateKey(block.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	policy, e := (rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}).Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	tx, e := f.FastTransaction(0, 1, policy)
	if e != nil {
		t.Fatal(e)
	}
	cert, e := f.DirectCertificate(tx, policy)
	if e != nil {
		t.Fatal(e)
	}
	certRaw, _ := cert.MarshalBinary()
	req := protocol.DirectRequest{Tx: tx}
	reqRaw, _ := req.MarshalBinary()
	paymentRaw, _ := (protocol.DirectPayment{Tx: tx, Certificate: cert}).MarshalBinary()
	finished := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(certRaw)
		w.(http.Flusher).Flush()
		finished <- struct{}{}
	}))
	defer upstream.Close()
	member := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-E5-Observe-Install") != "1" {
			t.Error("missing classification request")
		}
		w.Header().Set("X-E5-Install-State", "already_observed")
		w.Write([]byte("installed"))
	}))
	defer member.Close()
	p := &e5Proxy{gate: newE5Gate(1), mode: "B", config: e5ProxyConfig{Gateway: upstream.URL, Members: [4]string{member.URL, member.URL, member.URL, member.URL}}, network: cfg.Network{Organizations: []protocol.OrgConfig{f.Org}}, client: upstream.Client(), times: map[e5RequestKey]*e5Timing{}}
	p.network.Genesis.Network = tx.Body.Network
	proxy := httptest.NewServer(p)
	defer proxy.Close()
	done := make(chan error, 1)
	go func() {
		resp, err := proxy.Client().Post(proxy.URL+"/gateway/v3/transactions", "application/octet-stream", bytes.NewReader(reqRaw))
		if err == nil {
			b, e := io.ReadAll(resp.Body)
			resp.Body.Close()
			err = e
			if resp.StatusCode != 200 || !bytes.Equal(b, certRaw) {
				err = protocol.ErrRule
			}
		}
		done <- err
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("upstream blocked behind gate")
	}
	for i := 0; i < 3; i++ {
		resp, e := proxy.Client().Post(proxy.URL+"/member/"+string(rune('0'+i))+"/v3/certificates", "application/octet-stream", bytes.NewReader(paymentRaw))
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	select {
	case e := <-done:
		t.Fatalf("AlreadyObserved incorrectly released response: %v", e)
	case <-time.After(20 * time.Millisecond):
	}
	k, d, e := e5Identity(tx.Body.Network, req)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.gate.Public(k, d, cert.QC.Fact); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("verified public result did not release response")
	}
}
