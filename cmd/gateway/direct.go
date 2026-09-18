package main

import (
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"utxo/internal/gateway"
	"utxo/internal/transport"
	"utxo/protocol"
)

func directPaymentHandler(c *gateway.Collector) http.HandlerFunc {
	slots := make(chan struct{}, 128)
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "LIMITED", 429)
			return
		}
		if r.Header.Get("Content-Type") != transport.MediaType {
			http.Error(w, "INVALID_ENCODING", 400)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, protocol.MaxRequestBytes))
		if err != nil {
			http.Error(w, "INVALID_ENCODING", 400)
			return
		}
		req, err := protocol.DecodeDirectRequest(raw)
		if err != nil {
			http.Error(w, "INVALID_ENCODING", 400)
			return
		}
		cert, err := c.CollectDirect(r.Context(), req)
		if err != nil {
			http.Error(w, "QUORUM_UNAVAILABLE", 503)
			return
		}
		raw, err = cert.MarshalBinary()
		if err != nil {
			http.Error(w, "INVALID_CERTIFICATE", 500)
			return
		}
		w.Header().Set("Content-Type", transport.MediaType)
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
		_, _ = w.Write(raw)
		_ = http.NewResponseController(w).Flush()
		if err = c.PersistDirect(protocol.DirectPayment{Tx: req.Tx, Certificate: cert, Parents: req.Parents}); err != nil {
			slog.Error("background v3 certificate persistence failed", "error", err)
		}
	}
}
