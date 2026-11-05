package transport

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"utxo/internal/member"
	"utxo/protocol"
)

func addApprovalFeedHandler(mux *http.ServeMux, m *member.Member, bg chan struct{}, wrap func(chan struct{}, http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /v3/approved", wrap(bg, func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("after")
		if len(cursor) > 128 {
			fail(w, protocol.ErrEncoding)
			return
		}
		after, err := hex.DecodeString(cursor)
		if err != nil {
			fail(w, protocol.ErrEncoding)
			return
		}
		page, err := m.ApprovedDirect(after)
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	}))
}

func (c *MemberClient) ApprovedDirect(ctx context.Context, after []byte) (protocol.ApprovalPage, error) {
	var page protocol.ApprovalPage
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v3/approved?after="+hex.EncodeToString(after), nil)
	if err != nil {
		return page, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return page, err
	}
	defer resp.Body.Close()
	const limit = int64(protocol.ApprovalFeedPageSize) * int64(protocol.MaxRequestBytes) * 8
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return page, protocol.ErrRule
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return page, err
	}
	if int64(len(raw)) > limit {
		return page, protocol.ErrEncoding
	}
	if err = json.Unmarshal(raw, &page); err != nil {
		return page, err
	}
	if len(page.Items) > protocol.ApprovalFeedPageSize || len(page.Next) > 64 {
		return protocol.ApprovalPage{}, protocol.ErrEncoding
	}
	return page, nil
}
