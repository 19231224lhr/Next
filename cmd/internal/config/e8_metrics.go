package config

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"utxo/internal/requesttrace"
	"utxo/internal/store"
	"utxo/protocol"
)

type e8Bytes struct{ Requests, RequestBytes, ResponseBytes uint64 }
type e8Reader struct {
	io.ReadCloser
	n uint64
}

func (r *e8Reader) Read(p []byte) (int, error) {
	n, e := r.ReadCloser.Read(p)
	r.n += uint64(n)
	return n, e
}

type e8Writer struct {
	http.ResponseWriter
	n uint64
}

func (w *e8Writer) Write(p []byte) (int, error) {
	n, e := w.ResponseWriter.Write(p)
	w.n += uint64(n)
	return n, e
}
func (w *e8Writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *e8Writer) Flush()                      { _ = http.NewResponseController(w.ResponseWriter).Flush() }

// Each request body is counted at its receiving server and each response at
// its sender. This is HTTP payload, not P2P traffic or wire-byte accounting.
func e8HTTP(next http.Handler) http.Handler {
	var mu sync.Mutex
	totals := map[string]e8Bytes{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/debug/e8" {
			mu.Lock()
			snapshot := make(map[string]e8Bytes, len(totals))
			for k, v := range totals {
				snapshot[k] = v
			}
			mu.Unlock()
			var sizes map[string]map[uint8][2]uint64
			if r.URL.Query().Get("state") == "1" {
				sizes = store.E8StateSizes()
			}
			_ = json.NewEncoder(w).Encode(struct {
				Descriptor protocol.DescriptorCounters
				HTTP       map[string]e8Bytes
				Blocks     []requesttrace.E8Block
				State      map[string]map[uint8][2]uint64
			}{protocol.DescriptorMetrics(), snapshot, requesttrace.E8Blocks(), sizes})
			return
		}
		rd := &e8Reader{ReadCloser: r.Body}
		r.Body = rd
		wr := &e8Writer{ResponseWriter: w}
		next.ServeHTTP(wr, r)
		category := "other"
		for _, p := range []string{"/v4/progress", "/v3/transactions", "/v3/install", "/v4", "/v3", "/blocks", "/block", "/submit"} {
			if strings.HasPrefix(r.URL.Path, p) {
				category = p
				break
			}
		}
		mu.Lock()
		x := totals[category]
		x.Requests++
		x.RequestBytes += rd.n
		x.ResponseBytes += wr.n
		totals[category] = x
		mu.Unlock()
	})
}
