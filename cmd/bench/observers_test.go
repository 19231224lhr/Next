package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"utxo/internal/testkit"
	"utxo/internal/transport"
	"utxo/protocol"
)

func TestProofObservationDoesNotWaitForMemberCredit(t *testing.T) {
	f := testkit.NewFixture("observer", "a", 1)
	c, e := f.Certify(f.Transaction(0, 1))
	if e != nil {
		t.Fatal(e)
	}
	fact := protocol.FinalFact{Network: f.Org.Network, Rules: c.Tx.Body.Rules, Kind: protocol.FactFeeClosed, Key: protocol.Hash(c.Effects.Fee), Revision: 1, Payload: []byte("fee")}
	trust, proof, e := testkit.Proof("observer", fact)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := proof.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
	defer public.Close()
	entered := make(chan struct{}, 1)
	member := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-r.Context().Done() }))
	defer member.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := runner{ctx: ctx, trust: trust, http: member.Client(), public: transport.NewCommitteeClient(public.URL)}
	r.network.Members = map[protocol.Hash][4]string{c.Tx.Body.Certifier: {member.URL, member.URL, member.URL, member.URL}}
	j := job{cert: c, sample: measurement{Started: time.Now().Add(-time.Millisecond), Ready: time.Microsecond, Spend: protocol.Hash(c.QC.Fact).String()}}
	creditDone := make(chan measurement, 1)
	go func() { creditDone <- r.trackCredit(j) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("credit observer did not start")
	}
	for i := 0; i < 2; i++ {
		m := r.trackProof(j)
		if m.Error != "" || m.Proof == 0 || m.Closed != 0 || m.ProofQueue <= 0 {
			t.Fatalf("proof depends on credit: %+v", m)
		}
	}
	select {
	case <-creditDone:
		t.Fatal("credit unexpectedly completed")
	default:
	}
	cancel()
	<-creditDone
}
