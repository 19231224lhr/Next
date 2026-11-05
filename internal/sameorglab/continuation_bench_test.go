package sameorglab_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"
	"utxo/internal/store"
	"utxo/protocol"
)

type continuationRun struct {
	Topology, StoreMode        string
	RequestLinkKbps            int
	ResponseLinkKbps           int
	Mode                       string
	Round                      int
	ReadyMS                    []float64
	CollectMS                  []float64
	Bytes, Requests, Fallbacks int64
	ResponseBytes              int64
	WorkloadHash               string
}

// Test-only body pacing: each member request has an independent limited link.
// This isolates payload serialization cost; it is not a WAN deployment model.
type pacedTransport struct {
	base                   http.RoundTripper
	bytesPerSecond         int64
	responseBytesPerSecond int64
}

func (p pacedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	if p.bytesPerSecond > 0 {
		c.Body = &pacedBody{ReadCloser: r.Body, ctx: r.Context(), rate: p.bytesPerSecond, start: time.Now()}
	}
	response, e := p.base.RoundTrip(c)
	if e == nil && p.responseBytesPerSecond > 0 {
		response.Body = &pacedBody{ReadCloser: response.Body, ctx: r.Context(), rate: p.responseBytesPerSecond, start: time.Now()}
	}
	return response, e
}

type pacedBody struct {
	io.ReadCloser
	ctx         context.Context
	rate, total int64
	start       time.Time
}

func (p *pacedBody) Read(b []byte) (int, error) {
	n, e := p.ReadCloser.Read(b)
	p.total += int64(n)
	wait := time.Until(p.start.Add(time.Duration(p.total * int64(time.Second) / p.rate)))
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-p.ctx.Done():
			return 0, p.ctx.Err()
		case <-timer.C:
		}
	}
	return n, e
}

func TestContinuationExperiment(t *testing.T) {
	path := os.Getenv("NEXT_CONTINUATION_RESULT")
	if path == "" {
		t.Skip("opt-in comparison")
	}
	const n = 100
	kbps, _ := strconv.Atoi(os.Getenv("NEXT_CONTINUATION_KBPS"))
	responseKbps, _ := strconv.Atoi(os.Getenv("NEXT_CONTINUATION_RESPONSE_KBPS"))
	storeMode := os.Getenv("NEXT_CONTINUATION_STORE")
	if storeMode == "" {
		storeMode = "memory"
	}
	topology := os.Getenv("NEXT_CONTINUATION_TOPOLOGY")
	if topology == "" {
		topology = "same"
	}
	factory := func(role, path string, id store.Identity) (store.Store, error) {
		if storeMode == "durable" {
			return store.Open(path, id)
		}
		return store.NewMemory(), nil
	}
	makeLabs := func(t *testing.T, v variant) [2]*lab {
		if topology == "cross" {
			return newCrossLabs(t, v, factory)
		}
		l := newLabWithStore(t, v, factory)
		return [2]*lab{l, l}
	}
	var plan []protocol.FastTx
	t.Run("prepare", func(t *testing.T) {
		labs := makeLabs(t, variants[0])
		var prev *protocol.DirectPayment
		for i := 0; i < n; i++ {
			l := labs[i%2]
			r := l.request(t, i, prev)
			c, e := l.f.DirectCertificate(r.Tx, l.policy)
			must(t, e)
			plan = append(plan, r.Tx)
			prev = &protocol.DirectPayment{Tx: r.Tx, Certificate: c}
		}
	})
	var runs []continuationRun
	configs := []variant{{name: "baseline"}, {name: "continuation", continuation: true}, {name: "compact-response", compactReply: true}, {name: "combined", continuation: true, compactReply: true}}
	rounds := 8
	if r, e := strconv.Atoi(os.Getenv("NEXT_CONTINUATION_ROUNDS")); e == nil && r > 0 {
		rounds = r
	}
	for round := 0; round < rounds; round++ {
		for offset := range configs {
			v := configs[(round+offset)%len(configs)]
			name := v.name
			t.Run(fmt.Sprintf("r%d-%s", round, name), func(t *testing.T) {
				labs := makeLabs(t, v)
				unique := []*lab{labs[0]}
				if labs[1] != labs[0] {
					unique = append(unique, labs[1])
				}
				if kbps > 0 || responseKbps > 0 {
					for _, l := range unique {
						for _, c := range l.clients {
							c.HTTP.Transport = pacedTransport{base: c.HTTP.Transport, bytesPerSecond: int64(kbps) * 1000 / 8, responseBytesPerSecond: int64(responseKbps) * 1000 / 8}
						}
					}
				}
				r := continuationRun{Mode: name, Round: round, RequestLinkKbps: kbps, ResponseLinkKbps: responseKbps, Topology: topology, StoreMode: storeMode}
				var prev *protocol.DirectPayment
				var ids []byte
				for i, tx := range plan {
					l := labs[i%2]
					req := protocol.DirectRequest{Tx: tx}
					if prev != nil {
						req.InputCertificates = []protocol.InputCertificate{{Certificate: prev.Certificate, Index: 0}}
					}
					start := time.Now()
					cert, e := l.collector.CollectDirect(context.Background(), req)
					must(t, e)
					r.CollectMS = append(r.CollectMS, float64(time.Since(start).Nanoseconds())/1e6)
					must(t, labs[1-i%2].wallets[1-i%2].ReceiveDirect(tx.Body.Outputs[0], cert, 0))
					r.ReadyMS = append(r.ReadyMS, float64(time.Since(start).Nanoseconds())/1e6)
					prev = &protocol.DirectPayment{Tx: tx, Certificate: cert, InputCertificates: req.InputCertificates}
					id := tx.ID()
					ids = append(ids, id[:]...)
				}
				// Let cancelled fourth requests finish outside the measured interval.
				time.Sleep(20 * time.Millisecond)
				for _, l := range unique {
					r.Bytes += l.bytes.Load()
					r.Requests += l.requests.Load()
					r.Fallbacks += l.misses.Load()
					r.ResponseBytes += l.responseBytes.Load()
				}
				r.WorkloadHash = protocol.Digest("CONTINUATION_WORKLOAD", ids).String()
				runs = append(runs, r)
			})
		}
	}
	raw, e := json.MarshalIndent(runs, "", "  ")
	must(t, e)
	must(t, os.WriteFile(path, raw, 0600))
}
