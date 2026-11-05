package main

import (
	"context"
	"net/http"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/requesttrace"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

// The wallet talks directly to GMs. Keep this helper's result shape compatible
// with historical timing drivers without routing a request through a collector.
func submitWalletDirect(ctx context.Context, client *http.Client, n cfg.Network, raw []byte, trace bool, beforeSend ...func(time.Time, int) error) ([]byte, http.Header, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if trace {
		ctx = requesttrace.Start(ctx, "wallet")
	}
	request, err := protocol.DecodeDirectRequest(raw)
	if err != nil {
		return nil, nil, 0, err
	}
	if n.Direct != nil && n.Direct.RequireApprovalDeadline && request.Tx.Body.ApprovalDeadline == 0 {
		return nil, nil, 0, protocol.ErrRule
	}
	if deadline := request.Tx.Body.ApprovalDeadline; deadline != 0 && time.Now().Unix() >= deadline {
		return nil, nil, 0, protocol.ErrExpired
	}
	org, err := n.Organization(request.Tx.Body.Certifier)
	if err != nil {
		return nil, nil, 0, err
	}
	var members [4]wallet.DirectApprover
	for i, url := range n.Members[org.Org] {
		m := transport.NewMemberClient(url)
		m.HTTP = client
		members[i] = m
	}
	for attempt := 1; ; attempt++ {
		if len(beforeSend) > 0 {
			if err := beforeSend[0](time.Now(), attempt); err != nil {
				return nil, nil, attempt, err
			}
		}
		cert, err := wallet.CollectDirect(ctx, org, members, request)
		if err == nil {
			b, e := cert.MarshalBinary()
			h := make(http.Header)
			if trace {
				h.Set(requesttrace.HeaderName, requesttrace.Header(ctx))
			}
			return b, h, attempt, e
		}
		select {
		case <-ctx.Done():
			return nil, nil, attempt, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
