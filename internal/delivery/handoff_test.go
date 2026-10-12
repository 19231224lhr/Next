package delivery

import (
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
	"utxo/internal/store"
	"utxo/internal/wallet"
	"utxo/protocol"
)

// Opt-in reproducible networked handoff experiment. Both modes end after the
// identical wallet Handler verifies the complete certificate and commits to Bolt.
// This isolates post-QC delivery, not consensus or an entire WAN deployment.
func TestNetworkHandoffExperiment(t *testing.T) {
	if os.Getenv("UTXO_DELIVERY_EXPERIMENT") != "1" {
		t.Skip("set UTXO_DELIVERY_EXPERIMENT=1")
	}
	f, p := fixture(t)
	owner := p.Tx.Body.Outputs[0].Recipient.Owner
	const samples = 20
	fmt.Println("delivery_csv,mode,sender_response_delay_ms,recipient_delay_ms,sample,verified_persisted_ms,sender_response_ms")
	for _, delay := range []time.Duration{0, 20 * time.Millisecond, 50 * time.Millisecond} {
		for _, recipientDelay := range []time.Duration{0, 20 * time.Millisecond} {
			for _, direct := range []bool{false, true} {
				dir := t.TempDir()
				db, e := store.Open(filepath.Join(dir, "wallet.db"), store.Identity{Network: "delivery", Role: "wallet", Node: "1", Schema: 1})
				if e != nil {
					t.Fatal(e)
				}
				local, e := wallet.NewWithGenesis(db, f.Org.Network, owner, []protocol.OrgConfig{f.Org}, f.Genesis)
				if e != nil {
					t.Fatal(e)
				}
				received := make(chan time.Time, 1)
				h := Handler(map[protocol.PublicKey]*wallet.Wallet{owner: local}, nil)
				recipient := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					time.Sleep(recipientDelay)
					h.ServeHTTP(w, r)
					received <- time.Now()
				}))
				outDB, e := store.Open(filepath.Join(dir, "outbox.db"), store.Identity{Network: "delivery", Role: "outbox", Node: "1", Schema: 1})
				if e != nil {
					t.Fatal(e)
				}
				q, e := New(outDB, []Route{{Owner: owner, URL: recipient.URL}}, []protocol.OrgConfig{f.Org})
				if e != nil {
					t.Fatal(e)
				}
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() { done <- q.Run(ctx) }()
				gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if direct {
						if e := q.Enqueue(p, ""); e != nil {
							http.Error(w, e.Error(), 500)
							return
						}
					}
					time.Sleep(delay)
					raw, e := p.MarshalBinary()
					if e != nil {
						http.Error(w, e.Error(), 500)
						return
					}
					w.Write(raw)
				}))
				var times []float64
				mode := "sender-forward"
				if direct {
					mode = "gateway-direct"
				}
				for i := 0; i < samples; i++ {
					// Changing nonce produces a fresh recipient coin, so every sample includes a write.
					p.Tx.Body.Nonce[0] = byte(i + 1)
					p.Tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(p.Tx.ID(), f.Owner)}
					p.Certificate.Summary = protocol.SummaryFor(p.Tx, p.Certificate.Summary.Admission)
					p.Certificate.QC.Fact = p.Certificate.Summary.Fact()
					p.Certificate.QC.Votes = nil
					for j := 0; j < 3; j++ {
						p.Certificate.QC.Votes = append(p.Certificate.QC.Votes, protocol.SignSpend(p.Certificate.QC.Fact, uint16(j), f.Keys[j]))
					}
					start := time.Now()
					resp, e := gateway.Client().Post(gateway.URL, "application/octet-stream", nil)
					if e != nil {
						t.Fatal(e)
					}
					raw, e := io.ReadAll(resp.Body)
					resp.Body.Close()
					if e != nil || resp.StatusCode != 200 {
						t.Fatalf("gateway: %d %v", resp.StatusCode, e)
					}
					sender := time.Since(start)
					if !direct {
						if e = send(ctx, recipient.Client(), recipient.URL, record{Payment: raw, Index: 0}); e != nil {
							t.Fatal(e)
						}
					}
					var end time.Time
					select {
					case end = <-received:
					case <-time.After(3 * time.Second):
						t.Fatal("delivery timeout")
					}
					ms := float64(end.Sub(start)) / float64(time.Millisecond)
					times = append(times, ms)
					fmt.Printf("delivery_csv,%s,%d,%d,%d,%.3f,%.3f\n", mode, delay.Milliseconds(), recipientDelay.Milliseconds(), i, ms, float64(sender)/float64(time.Millisecond))
				}
				sort.Float64s(times)
				t.Logf("%s response_delay=%dms recipient_delay=%dms n=%d median=%.3fms p95=%.3fms", mode, delay.Milliseconds(), recipientDelay.Milliseconds(), samples, times[samples/2], times[18])
				gateway.Close()
				cancel()
				if e = <-done; e != nil {
					t.Fatal(e)
				}
				recipient.Close()
				outDB.Close()
				db.Close()
			}
		}
	}
}
