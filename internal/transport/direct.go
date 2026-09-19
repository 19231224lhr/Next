package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"utxo/internal/member"
	"utxo/internal/requesttrace"
	"utxo/protocol"
)

func addDirectHandlers(mux *http.ServeMux, m *member.Member, fg, bg chan struct{}, wrap func(chan struct{}, http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("POST /v3/transactions", wrap(fg, func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if r.Header.Get(requesttrace.HeaderName) == "1" {
			ctx = requesttrace.Start(ctx, "member")
		}
		requesttrace.Mark(ctx, "http_handler_enter")
		raw, err := binaryBody(w, r, protocol.MaxRequestBytes)
		if err != nil {
			fail(w, err)
			return
		}
		req, err := protocol.DecodeDirectRequest(raw)
		if err != nil {
			fail(w, err)
			return
		}
		requesttrace.Mark(ctx, "request_decoded")
		approval, err := m.ApproveDirect(ctx, req)
		if err != nil {
			fail(w, err)
			return
		}
		requesttrace.Mark(ctx, "response_ready")
		if requesttrace.Enabled(ctx) {
			w.Header().Set(requesttrace.HeaderName, requesttrace.Header(ctx))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(approval)
	}))
	mux.HandleFunc("POST /v3/certificates", wrap(bg, func(w http.ResponseWriter, r *http.Request) {
		raw, err := binaryBody(w, r, protocol.MaxRequestBytes)
		if err != nil {
			fail(w, err)
			return
		}
		payment, err := protocol.DecodeDirectPayment(raw)
		if err == nil {
			err = m.InstallDirect(payment)
		}
		if err != nil {
			fail(w, err)
			return
		}
		binaryResponse(w, []byte("installed"))
	}))
}
func (c *MemberClient) ApproveDirect(ctx context.Context, r protocol.DirectRequest) (a protocol.DirectApproval, err error) {
	raw, err := r.MarshalBinary()
	if err != nil {
		return a, err
	}
	raw, err = c.post(ctx, "/v3/transactions", raw, 256*1024)
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(raw, &a)
	return a, err
}
func (c *MemberClient) InstallDirect(ctx context.Context, p protocol.DirectPayment) error {
	raw, err := p.MarshalBinary()
	if err != nil {
		return err
	}
	_, err = c.post(ctx, "/v3/certificates", raw, 1024)
	return err
}
