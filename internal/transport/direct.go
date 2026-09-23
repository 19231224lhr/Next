package transport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"utxo/internal/member"
	"utxo/internal/requesttrace"
	"utxo/protocol"
)

func addDirectHandlers(mux *http.ServeMux, m *member.Member, fg, bg chan struct{}, wrap func(chan struct{}, http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("POST /v4/progress", wrap(bg, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			fail(w, protocol.ErrEncoding)
			return
		}
		var facts []protocol.SpendFactID
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		if err := d.Decode(&facts); err != nil {
			fail(w, err)
			return
		}
		if err := d.Decode(new(any)); err != io.EOF {
			fail(w, protocol.ErrEncoding)
			return
		}
		statuses, err := m.DirectStatuses(facts)
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(statuses)
	}))
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
		approval, err := m.ApproveDirectBytes(ctx, raw)
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
		stored := false
		if err == nil {
			stored, err = m.InstallDirectClassified(payment)
		}
		if err != nil {
			fail(w, err)
			return
		}
		if r.Header.Get("X-E5-Observe-Install") == "1" {
			result := "already_observed"
			if stored {
				result = "stored"
			}
			w.Header().Set("X-E5-Install-State", result)
		}
		binaryResponse(w, []byte("installed"))
	}))
}
func (c *MemberClient) ApproveDirect(ctx context.Context, r protocol.DirectRequest) (protocol.DirectApproval, error) {
	raw, err := r.MarshalBinary()
	if err != nil {
		return protocol.DirectApproval{}, err
	}
	return c.ApproveDirectBytes(ctx, raw)
}

// ApproveDirectBytes sends the collector's immutable request encoding. The
// receiving member performs the same decoding and authorization checks.
func (c *MemberClient) ApproveDirectBytes(ctx context.Context, raw []byte) (a protocol.DirectApproval, err error) {
	requesttrace.MarkNode(ctx, c.BaseURL, "client_approve_enter")
	requesttrace.MarkNode(ctx, c.BaseURL, "request_serialized")
	raw, err = c.post(ctx, "/v3/transactions", raw, 256*1024)
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(raw, &a)
	requesttrace.MarkNode(ctx, c.BaseURL, "approval_decoded")
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
