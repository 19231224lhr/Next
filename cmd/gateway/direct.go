package main

import (
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"utxo/internal/gateway"
	"utxo/internal/requesttrace"
	"utxo/internal/transport"
	"utxo/protocol"
)

func directPaymentHandler(c *gateway.Collector) http.HandlerFunc {
	slots := make(chan struct{}, 128)
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if r.Header.Get(requesttrace.HeaderName) == "1" {
			ctx = requesttrace.Start(ctx, "gateway")
		}
		requesttrace.Mark(ctx, "http_handler_enter")
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
		requesttrace.Mark(ctx, "request_decoded")
		cert, err := c.CollectDirect(ctx, req)
		if err != nil {
			http.Error(w, "QUORUM_UNAVAILABLE", 503)
			return
		}
		raw, err = cert.MarshalBinary()
		if err != nil {
			http.Error(w, "INVALID_CERTIFICATE", 500)
			return
		}
		requesttrace.Mark(ctx, "response_ready")
		requesttrace.Payment("certificate_ready", cert.QC.Fact)
		if requesttrace.Enabled(ctx) {
			w.Header().Set(requesttrace.HeaderName, requesttrace.Header(ctx))
		}
		w.Header().Set("Content-Type", transport.MediaType)
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
		_, _ = w.Write(raw)
		_ = http.NewResponseController(w).Flush()
		payment := protocol.DirectPayment{Tx: req.Tx, Certificate: cert, InputCertificates: req.InputCertificates}
		if c.OfferDirect != nil {
			c.OfferDirect(payment)
		}
		requesttrace.Payment("outbox_persist_start", cert.QC.Fact)
		if err = c.PersistDirect(payment); err != nil {
			slog.Error("background v3 certificate persistence failed", "error", err)
		} else {
			requesttrace.Payment("outbox_persist_done", cert.QC.Fact)
		}
	}
}
