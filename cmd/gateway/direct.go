package main

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"utxo/internal/delivery"
	"utxo/internal/gateway"
	"utxo/internal/requesttrace"
	"utxo/internal/transport"
	"utxo/protocol"
)

const directPersistenceLimit = 128

// drain closes admission, joins handlers (including synchronous fallback), then
// joins saves. Call it before closing the collector's store, even if HTTP shutdown times out.
func directPaymentHandler(c *gateway.Collector) (http.HandlerFunc, func()) {
	return newDirectPaymentHandler(c, true)
}

func newDirectPaymentHandler(c *gateway.Collector, publish bool) (http.HandlerFunc, func()) {
	return newDirectPaymentHandlerWithDelivery(c, publish, nil)
}

func newDirectPaymentHandlerWithDelivery(c *gateway.Collector, publish bool, outbox *delivery.Outbox) (http.HandlerFunc, func()) {
	slots := make(chan struct{}, 128)
	saves := make(chan struct{}, directPersistenceLimit)
	var pending sync.WaitGroup
	var lifecycle sync.RWMutex
	closed := false
	persist := func(payment protocol.DirectPayment) {
		fact := payment.Certificate.QC.Fact
		requesttrace.Payment("outbox_persist_start", fact)
		if err := c.PersistDirect(payment); err != nil {
			slog.Error("background v3 certificate persistence failed", "spend", protocol.Hash(fact).String(), "error", err)
		} else {
			requesttrace.Payment("outbox_persist_done", fact)
		}
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		lifecycle.RLock()
		defer lifecycle.RUnlock()
		if closed {
			http.Error(w, "SHUTTING_DOWN", http.StatusServiceUnavailable)
			return
		}
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
		if outbox != nil {
			if len(r.Header.Get(delivery.MetadataHeader)) > delivery.MaxMetadata || outbox.CheckRoutes(req.Tx) != nil {
				http.Error(w, "DELIVERY_ROUTE_UNAVAILABLE", 400)
				return
			}
		}
		cert, err := c.CollectDirect(ctx, req)
		if err != nil {
			if errors.Is(err, gateway.ErrSigningBusy) {
				http.Error(w, "SIGNING_BUSY", http.StatusTooManyRequests)
				return
			}
			if ctx.Err() == nil {
				slog.Warn("direct approval quorum unavailable", "tx", req.Tx.ID(), "error", err)
			}
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
		payment := protocol.DirectPayment{Tx: req.Tx, Certificate: cert, InputCertificates: req.InputCertificates}
		if outbox != nil {
			if err = outbox.Enqueue(payment, r.Header.Get(delivery.MetadataHeader)); err != nil {
				// A valid QC already exists. Preserve public recovery even when delivery
				// storage fails; the client must retry the same signed request.
				persist(payment)
				http.Error(w, "DELIVERY_PERSIST_FAILED_RETRY_SAME_REQUEST", 503)
				return
			}
		}
		if requesttrace.Enabled(ctx) {
			w.Header().Set(requesttrace.HeaderName, requesttrace.Header(ctx))
		}
		w.Header().Set("Content-Type", transport.MediaType)
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
		_, _ = w.Write(raw)
		_ = http.NewResponseController(w).Flush()
		if !publish {
			return
		} // E2 controls parent publication independently.
		if c.OfferDirect != nil {
			c.OfferDirect(payment)
		}
		// The decoded payment is immutable and owns its data. No request context or
		// ResponseWriter escapes. Concurrent saves retain the existing Group batching.
		select {
		case saves <- struct{}{}:
			requesttrace.Payment("persist_background_queued", cert.QC.Fact)
			pending.Add(1)
			go func() {
				defer pending.Done()
				defer func() {
					requesttrace.Payment("persist_background_finished", cert.QC.Fact)
					<-saves
				}()
				persist(payment)
			}()
		default:
			// ponytail: bounded overload keeps the old synchronous path; never drop a save.
			requesttrace.Payment("persist_background_full", cert.QC.Fact)
			persist(payment)
		}
	}
	return handler, func() {
		lifecycle.Lock()
		closed = true
		lifecycle.Unlock()
		pending.Wait()
	}
}
