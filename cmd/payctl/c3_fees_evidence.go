package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
	"utxo/internal/transport"
	"utxo/protocol"
)

// Amounts come from verified wallet outputs. A present refund is not silently
// replaced with the expected zero, even when its presence invalidates C3.
func c3ObservedFeeNet(h c3FeePayment) (uint64, error) {
	returned := h.FeeChange.Output.Amount
	if h.FeeRefund != nil {
		var e error
		returned, e = protocol.Add(returned, h.FeeRefund.Output.Amount)
		if e != nil {
			return 0, e
		}
	}
	if returned > h.FeeInput.Output.Amount {
		return 0, fmt.Errorf("verified fee outputs exceed input")
	}
	net := h.FeeInput.Output.Amount - returned
	if h.FeeRefund != nil {
		return net, fmt.Errorf("unexpected C3 fee refund: %d", h.FeeRefund.Output.Amount)
	}
	if net != 10 {
		return net, fmt.Errorf("user net fee is %d", net)
	}
	return net, nil
}

type c3SubmitAttempt struct {
	StartOffsetNS, EndOffsetNS int64
	Accepted                   bool
	Error                      string `json:",omitempty"`
}

// This records CommitteeClient calls, which can each try several endpoints.
// The transport does not expose per-endpoint status; its returned error is kept.
func c3SubmitRecorded(ctx context.Context, start time.Time, raw []byte, submit func(context.Context, []byte) error, rows *[]c3SubmitAttempt) error {
	r := c3SubmitAttempt{StartOffsetNS: time.Since(start).Nanoseconds()}
	e := submit(ctx, raw)
	r.EndOffsetNS = time.Since(start).Nanoseconds()
	r.Accepted = e == nil
	if e != nil {
		r.Error = e.Error()
	}
	*rows = append(*rows, r)
	return e
}

type c3ReplayInclusion struct {
	Height          int64
	Index           int
	BlockHash       []byte
	Code            uint32
	ResultData      []byte
	EconomicApplied *bool `json:",omitempty"`
}
type c3ReplayEvidence struct {
	Outcome, URL string
	HTTPStatus   int
	ResponseBody string
	Error        string             `json:",omitempty"`
	Inclusion    *c3ReplayInclusion `json:",omitempty"`
}

func c3FindReplay(blocks []c3FeeBlock, raw []byte, after int64) *c3ReplayInclusion {
	for _, b := range blocks {
		if b.Height <= after {
			continue
		}
		for i, t := range b.Transactions {
			if !bytes.Equal(t.Bytes, raw) {
				continue
			}
			r := &c3ReplayInclusion{Height: b.Height, Index: i, BlockHash: bytes.Clone(b.Hash), Code: t.Code, ResultData: bytes.Clone(t.Data)}
			for _, x := range b.Executions {
				if x.Index == i {
					applied := x.Result.Applied
					r.EconomicApplied = &applied
					break
				}
			}
			return r
		}
	}
	return nil
}

// A direct ordinary committee HTTP submission exposes the actual response for
// this one duplicate. A transport error cannot establish rejection.
func c3ReplaySubmit(ctx context.Context, client *http.Client, url string, raw []byte) (r c3ReplayEvidence, err error) {
	r.URL = url + "/v1/commands"
	r.Outcome = "transport-inconclusive"
	defer func() {
		if err != nil {
			r.Error = err.Error()
		}
	}()
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(raw))
	if e != nil {
		return r, e
	}
	req.Header.Set("Content-Type", transport.MediaType)
	resp, e := client.Do(req)
	if e != nil {
		return r, e
	}
	defer resp.Body.Close()
	r.HTTPStatus = resp.StatusCode
	body, e := io.ReadAll(io.LimitReader(resp.Body, 4096))
	r.ResponseBody = string(body)
	if e != nil {
		return r, e
	}
	r.Outcome = "http-rejected"
	if resp.StatusCode == http.StatusAccepted {
		r.Outcome = "accepted-pending"
	}
	return r, nil
}
