package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
	"utxo/internal/delivery"
	"utxo/internal/gateway"
	"utxo/internal/poollab"
	"utxo/internal/recipient"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

// Matched full-payment experiment using the actual gateway direct-delivery
// handler. Additional link delay is deliberately imposed, not a WAN measurement.
func TestRecipientCollectionExperiment(t *testing.T) {
	if os.Getenv("UTXO_RECIPIENT_EXPERIMENT") != "1" {
		t.Skip("set UTXO_RECIPIENT_EXPERIMENT=1")
	}
	const n = 50
	for round := 0; round < 3; round++ {
		for _, delay := range []time.Duration{0, 20 * time.Millisecond} {
			modes := []bool{false, true}
			if round%2 == 1 {
				modes = []bool{true, false}
			}
			for _, direct := range modes {
				t.Run(fmt.Sprintf("round%d_delay%d_receiver%v", round, delay.Milliseconds(), direct), func(t *testing.T) {
					l, e := poollab.New(t.TempDir())
					if e != nil {
						t.Fatal(e)
					}
					defer l.Close()
					open := func(name string) store.Store {
						db, e := store.Open(filepath.Join(t.TempDir(), name), store.Identity{Network: "recipient-experiment", Role: name, Node: "1", Schema: 1})
						if e != nil {
							t.Fatal(e)
						}
						t.Cleanup(func() { db.Close() })
						return db
					}
					db := open("collector")
					outDB := open("delivery")
					var clients [4]gateway.MemberClient
					for i, s := range l.Servers {
						clients[i] = transport.NewMemberClient(s.URL)
					}
					c, e := gateway.New(l.Fixture.Org, clients, db)
					if e != nil {
						t.Fatal(e)
					}
					type receipt struct {
						at   time.Time
						cert protocol.OutputCertificate
					}
					arrived := make(chan receipt, n)
					wallets := map[protocol.PublicKey]*wallet.Wallet{}
					var routes []delivery.Route
					var receiving [3]*httptest.Server
					var collecting [3]*httptest.Server
					for i := 0; i < 3; i++ {
						wallets[l.Descriptors[i].Owner] = l.Wallets[i]
					}
					h := delivery.Handler(wallets, func(p protocol.DirectPayment, _ uint32, _ string) error {
						arrived <- receipt{time.Now(), p.Certificate}
						return nil
					})
					for i := 0; i < 3; i++ {
						receiving[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(delay); h.ServeHTTP(w, r) }))
						defer receiving[i].Close()
						routes = append(routes, delivery.Route{Owner: l.Descriptors[i].Owner, URL: receiving[i].URL})
						rc, e := recipient.New(c, l.Wallets[i], l.Descriptors[i].Owner, l.Fixture.Org, db)
						if e != nil {
							t.Fatal(e)
						}
						defer rc.Close()
						collecting[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							rec := httptest.NewRecorder()
							rc.ServeHTTP(rec, r)
							if rec.Code == 200 {
								cert, e := protocol.DecodeOutputCertificate(rec.Body.Bytes())
								if e != nil {
									t.Error(e)
								} else {
									arrived <- receipt{time.Now(), cert}
								}
							}
							for k, v := range rec.Header() {
								w.Header()[k] = v
							}
							w.WriteHeader(rec.Code)
							_, _ = w.Write(rec.Body.Bytes())
						}))
						defer collecting[i].Close()
					}
					q, e := delivery.New(outDB, routes, []protocol.OrgConfig{l.Fixture.Org})
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
					gh, drain := newDirectPaymentHandlerWithDelivery(c, true, q)
					defer drain()
					gs := httptest.NewServer(gh)
					defer gs.Close()
					var previous protocol.OutputCertificate
					var prevOut protocol.Output
					owner := 0
					var samples []float64
					for i := 0; i < n; i++ {
						to := (owner + 1) % 3
						template, e := l.OrdinaryTemplate(owner, uint64(i+1))
						if e != nil {
							t.Fatal(e)
						}
						b := template.Body
						b.Subject = l.Descriptors[owner].Owner
						input := l.Fixture.Genesis.Outputs[0].Output
						var parents []protocol.InputCertificate
						if i > 0 {
							input = prevOut
							b.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: previous.Summary.OutputID(0), Evidence: protocol.Hash(previous.QC.Fact)}}
							parents = []protocol.InputCertificate{{Certificate: previous, Index: 0}}
						}
						out := protocol.Output{Asset: protocol.AssetCAL, Amount: 100, Recipient: l.Descriptors[to]}
						b.Outputs = []protocol.Output{out}
						tx, e := protocol.NewFastTx(b, []protocol.InputClaim{{Output: input}}, l.Policy.Key)
						if e != nil {
							t.Fatal(e)
						}
						tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), l.Keys[owner])}
						req := protocol.DirectRequest{Tx: tx, InputCertificates: parents}
						raw, e := req.MarshalBinary()
						if e != nil {
							t.Fatal(e)
						}
						started := time.Now()
						if e = l.Wallets[owner].SaveDirectRequest(req); e != nil {
							t.Fatal(e)
						}
						endpoint := gs.URL
						if direct {
							endpoint = collecting[to].URL
						}
						resp, e := http.Post(endpoint, transport.MediaType, bytes.NewReader(raw))
						if e != nil {
							t.Fatal(e)
						}
						response, e := io.ReadAll(resp.Body)
						resp.Body.Close()
						if e != nil || resp.StatusCode != 200 {
							t.Fatalf("request: %d %s %v", resp.StatusCode, response, e)
						}
						var got receipt
						select {
						case got = <-arrived:
						case <-time.After(5 * time.Second):
							t.Fatal("no receipt")
						}
						if got.cert.Summary.Tx != tx.ID() || got.at.Before(started) {
							t.Fatal("receipt does not match timed payment")
						}
						ms := float64(got.at.Sub(started).Nanoseconds()) / 1e6
						samples = append(samples, ms)
						fmt.Printf("recipient_csv,%d,%d,%v,%d,%.4f\n", round, delay.Milliseconds(), direct, i, ms)
						previous = got.cert
						prevOut = out
						owner = to
					}
					sort.Float64s(samples)
					t.Logf("round=%d extra_gateway_receiver_delay_ms=%d recipient_collector=%v n=%d p50_ms=%.4f p95_ms=%.4f", round, delay.Milliseconds(), direct, n, samples[(n-1)*50/100], samples[(n-1)*95/100])
				})
			}
		}
	}
}
