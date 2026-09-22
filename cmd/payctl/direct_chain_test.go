package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"utxo/internal/wallet"
	"utxo/protocol"
)

func TestChainRetryKeepsExactRequest(t *testing.T) {
	var bodies []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) == 1 {
			http.Error(w, "QUORUM_UNAVAILABLE", 503)
			return
		}
		w.Write([]byte("certificate"))
	}))
	defer s.Close()
	b, _, attempts, err := submitChain(context.Background(), s.Client(), s.URL, []byte("same signed transaction"), false)
	if err != nil || string(b) != "certificate" || attempts != 2 || len(bodies) != 2 || bodies[0] != bodies[1] {
		t.Fatalf("retry changed the signed transaction: %q %d %v %v", b, attempts, bodies, err)
	}
}

func TestChainDoesNotRetryRejectedAuthorization(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "INVALID_AUTH", 400) }))
	defer s.Close()
	_, _, attempts, err := submitChain(context.Background(), s.Client(), s.URL, []byte("rejected transaction"), false)
	if err == nil || attempts != 1 {
		t.Fatalf("permanent failure retried: %d %v", attempts, err)
	}
}

func TestChainInputUsesCurrentWalletState(t *testing.T) {
	id := protocol.OutputID(protocol.Digest("chain-test-output"))
	cert := &protocol.OutputCertificate{}
	cert.QC.Fact = protocol.SpendFactID(protocol.Digest("chain-test-fact"))
	coin := wallet.DirectCoin{Certificate: cert, Index: 2}
	in, proofs, err := chainInput(id, coin)
	if err != nil || in.Kind != protocol.CertificateInput || in.Output != id || in.Evidence != protocol.Hash(cert.QC.Fact) || len(proofs) != 1 || proofs[0].Index != 2 {
		t.Fatalf("unconfirmed coin lost its immediate certificate: %+v %v %v", in, proofs, err)
	}
	coin.Final = protocol.Digest("chain-test-final")
	in, proofs, err = chainInput(id, coin)
	if err != nil || in.Kind != protocol.FinalInput || in.Evidence != coin.Final || len(proofs) != 0 {
		t.Fatalf("final coin still depends on certificate: %+v %v %v", in, proofs, err)
	}
	if _, _, err = chainInput(id, wallet.DirectCoin{}); err == nil {
		t.Fatal("coin without finality or certificate accepted")
	}
}
