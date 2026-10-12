package transport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"utxo/internal/member"
	"utxo/protocol"
)

func addPoolHandlers(mux *http.ServeMux, m *member.Member, fg chan struct{}, wrap func(chan struct{}, http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("POST /v1/internal-pools/install", wrap(fg, func(w http.ResponseWriter, r *http.Request) {
		raw, err := binaryBody(w, r, 64<<10)
		if err != nil {
			fail(w, err)
			return
		}
		c, err := protocol.DecodePoolCertificate(raw)
		if err != nil {
			fail(w, err)
			return
		}
		if err = m.InstallPool(r.Context(), c); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /v1/internal-pools/certificates/{id}", wrap(fg, func(w http.ResponseWriter, r *http.Request) {
		var id protocol.Hash
		if err := id.UnmarshalText([]byte(r.PathValue("id"))); err != nil {
			fail(w, err)
			return
		}
		c, err := m.PoolCertificate(r.Context(), id)
		if err != nil {
			fail(w, err)
			return
		}
		raw, err := c.MarshalBinary()
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(raw)
	}))
	mux.HandleFunc("POST /v1/internal-pools/approve", wrap(fg, func(w http.ResponseWriter, r *http.Request) {
		raw, e := binaryBody(w, r, 128<<10)
		if e != nil {
			fail(w, e)
			return
		}
		request, e := protocol.DecodePoolRequest(raw)
		if e != nil {
			fail(w, e)
			return
		}
		vote, e := m.ApprovePool(r.Context(), request)
		if e != nil {
			fail(w, e)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(vote)
	}))
}

func (c *MemberClient) InstallPool(ctx context.Context, cert protocol.PoolCertificate) error {
	raw, err := cert.MarshalBinary()
	if err != nil {
		return err
	}
	_, err = c.post(ctx, "/v1/internal-pools/install", raw, 4096)
	return err
}

func (c *MemberClient) PoolCertificate(ctx context.Context, id protocol.Hash) (protocol.PoolCertificate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/internal-pools/certificates/"+id.String(), nil)
	if err != nil {
		return protocol.PoolCertificate{}, err
	}
	response, err := c.HTTP.Do(req)
	if err != nil {
		return protocol.PoolCertificate{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		return protocol.PoolCertificate{}, err
	}
	if len(raw) > 64<<10 {
		return protocol.PoolCertificate{}, protocol.ErrEncoding
	}
	if response.StatusCode != http.StatusOK {
		return protocol.PoolCertificate{}, &memberHTTPError{status: response.StatusCode, code: strings.TrimSpace(string(raw))}
	}
	return protocol.DecodePoolCertificate(raw)
}
func (c *MemberClient) ApprovePool(ctx context.Context, r protocol.PoolRequest) (v protocol.PoolVote, err error) {
	raw, err := r.MarshalBinary()
	if err != nil {
		return v, err
	}
	b, err := c.post(ctx, "/v1/internal-pools/approve", raw, 4096)
	if err != nil {
		return v, err
	}
	err = json.Unmarshal(b, &v)
	return
}
