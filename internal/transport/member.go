package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"utxo/finality"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

const MediaType = "application/vnd.utxofast.v2"

func ErrorCode(e error) (string, int) {
	switch {
	case errors.Is(e, rules.ErrLimited):
		return "LIMITED", http.StatusTooManyRequests
	case errors.Is(e, rules.ErrMissing), errors.Is(e, state.ErrNotFound):
		return "DEFERRED", http.StatusConflict
	case errors.Is(e, rules.ErrConflict):
		return "LOCAL_CONFLICT", http.StatusConflict
	case errors.Is(e, store.ErrUncertain), errors.Is(e, store.ErrClosed):
		return "STORE_UNCERTAIN", http.StatusServiceUnavailable
	case errors.Is(e, protocol.ErrAuth):
		return "INVALID_AUTH", http.StatusBadRequest
	case errors.Is(e, protocol.ErrUnsupported):
		return "UNSUPPORTED", http.StatusUnprocessableEntity
	default:
		return "INVALID_REQUEST", http.StatusBadRequest
	}
}
func fail(w http.ResponseWriter, e error) { code, status := ErrorCode(e); http.Error(w, code, status) }
func binaryBody(w http.ResponseWriter, r *http.Request, limit int) ([]byte, error) {
	if r.Header.Get("Content-Type") != MediaType {
		return nil, protocol.ErrEncoding
	}
	return io.ReadAll(http.MaxBytesReader(w, r.Body, int64(limit)))
}
func binaryResponse(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", MediaType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
func MemberHandler(m *member.Member, foreground, background int) http.Handler {
	mux := http.NewServeMux()
	fg := make(chan struct{}, foreground)
	bg := make(chan struct{}, background)
	wrap := func(tokens chan struct{}, handler http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			select {
			case tokens <- struct{}{}:
				defer func() { <-tokens }()
				handler(w, r)
			default:
				http.Error(w, "LIMITED", http.StatusTooManyRequests)
			}
		}
	}
	mux.HandleFunc("POST /v1/transactions", wrap(fg, func(w http.ResponseWriter, r *http.Request) {
		raw, e := binaryBody(w, r, protocol.MaxRequestBytes)
		if e != nil {
			fail(w, e)
			return
		}
		request, e := protocol.DecodeRequest(raw)
		if e != nil {
			fail(w, e)
			return
		}
		approval, e := m.Approve(request)
		if e != nil {
			fail(w, e)
			return
		}
		raw, e = approval.MarshalBinary()
		if e != nil {
			fail(w, e)
			return
		}
		binaryResponse(w, raw)
	}))
	mux.HandleFunc("POST /v1/certificates", wrap(bg, func(w http.ResponseWriter, r *http.Request) {
		raw, e := binaryBody(w, r, protocol.MaxCertificateBytes)
		if e != nil {
			fail(w, e)
			return
		}
		c, e := protocol.DecodeCertificate(raw)
		if e == nil {
			e = m.Install(c)
		}
		if e != nil {
			fail(w, e)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("POST /v1/receipts", wrap(bg, func(w http.ResponseWriter, r *http.Request) {
		raw, e := binaryBody(w, r, finality.MaxProofBytes)
		if e != nil {
			fail(w, e)
			return
		}
		p, e := finality.Decode(raw)
		if e == nil {
			e = m.ApplyProof(p)
		}
		if e != nil {
			fail(w, e)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("alive\n"))
	})
	return mux
}

type MemberClient struct {
	BaseURL string
	HTTP    *http.Client
}

func NewMemberClient(url string) *MemberClient {
	return &MemberClient{BaseURL: strings.TrimRight(url, "/"), HTTP: &http.Client{Timeout: 10 * time.Second}}
}
func (c *MemberClient) post(ctx context.Context, path string, raw []byte, limit int) ([]byte, error) {
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	request.Header.Set("Content-Type", MediaType)
	response, e := c.HTTP.Do(request)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, int64(limit+1)))
	if e != nil {
		return nil, e
	}
	if len(body) > limit {
		return nil, protocol.ErrEncoding
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("member HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}
func (c *MemberClient) Approve(ctx context.Context, r protocol.PaymentRequest) (protocol.Approval, error) {
	raw, e := r.MarshalBinary()
	if e != nil {
		return protocol.Approval{}, e
	}
	raw, e = c.post(ctx, "/v1/transactions", raw, 16384)
	if e != nil {
		return protocol.Approval{}, e
	}
	return protocol.DecodeApproval(raw)
}
func (c *MemberClient) Install(ctx context.Context, certificate protocol.TXCer) error {
	raw, e := certificate.MarshalBinary()
	if e != nil {
		return e
	}
	_, e = c.post(ctx, "/v1/certificates", raw, 1024)
	return e
}
func (c *MemberClient) ApplyProof(ctx context.Context, p finality.FactProof) error {
	raw, e := p.MarshalBinary()
	if e != nil {
		return e
	}
	_, e = c.post(ctx, "/v1/receipts", raw, 1024)
	return e
}
