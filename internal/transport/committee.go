package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"utxo/finality"
	"utxo/internal/requesttrace"
	"utxo/protocol"
)

type CommitteeClient struct {
	BaseURL    string
	HTTP       *http.Client
	submitURLs []string
}

func NewCommitteeClient(url string, extra ...string) *CommitteeClient {
	base := strings.TrimRight(url, "/")
	urls := []string{base}
	for _, other := range extra {
		other = strings.TrimRight(other, "/")
		duplicate := other == ""
		for _, existing := range urls {
			duplicate = duplicate || other == existing
		}
		if !duplicate {
			urls = append(urls, other)
		}
	}
	client := &CommitteeClient{BaseURL: base, HTTP: NewHTTPClient(5 * time.Second)}
	if len(urls) > 1 {
		client.submitURLs = urls
	}
	return client
}
func (c *CommitteeClient) Submit(ctx context.Context, raw []byte) error {
	_, directErr := protocol.DecodeDirectSubmission(raw)
	// A relay already persisted its envelope. Network retries must preserve it.
	if _, err := protocol.DecodeSubmission(raw); err != nil && directErr != nil && !protocol.IsRepairInput(raw) {
		var network protocol.Hash
		cert, e := protocol.DecodeCertificate(raw)
		if e == nil {
			network = cert.Tx.Body.Network
		} else {
			tx, e := protocol.DecodeSignedTx(raw)
			if e != nil {
				return e
			}
			network = tx.Body.Network
		}
		attempt := protocol.Submission{Network: network, Body: raw}
		if _, e = rand.Read(attempt.Nonce[:]); e != nil {
			return e
		}
		raw, e = attempt.MarshalBinary()
		if e != nil {
			return e
		}
	}

	urls := c.submitURLs
	if len(urls) == 0 {
		urls = []string{c.BaseURL}
	} // Preserve clients built as struct literals.
	start := 0
	if len(urls) > 1 {
		key := protocol.Digest("COMMITTEE_SUBMIT_ROUTE", raw)
		start = int(binary.BigEndian.Uint64(key[:8]) % uint64(len(urls)))
		if _, ok := ctx.Deadline(); !ok {
			budget := c.HTTP.Timeout
			if budget == 0 {
				budget = 5 * time.Second
			}
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, budget)
			defer cancel()
		}
	}
	var last error
	for offset := range urls {
		if err := ctx.Err(); err != nil {
			return err
		}
		url := urls[(start+offset)%len(urls)]
		attemptCtx := ctx
		cancelAttempt := func() {}
		if len(urls) > 1 {
			deadline, _ := ctx.Deadline()
			attemptCtx, cancelAttempt = context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(urls)-offset))
		}
		request, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, url+"/v1/commands", bytes.NewReader(raw))
		if err != nil {
			cancelAttempt()
			return err
		}
		request.Header.Set("Content-Type", MediaType)
		if requesttrace.Settlement != nil {
			request.Header.Set(requesttrace.DeliverySourceHeader, filepath.Base(os.Args[0]))
			request.Header.Set(requesttrace.DeliveryTimeHeader, strconv.FormatInt(time.Now().UnixNano(), 10))
		}
		response, err := c.HTTP.Do(request)
		if err != nil {
			cancelAttempt()
			last = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		cancelAttempt()
		if response.StatusCode == http.StatusAccepted {
			return nil
		}
		last = fmt.Errorf("committee submission HTTP %d", response.StatusCode)
		if response.StatusCode != http.StatusTooManyRequests && (response.StatusCode < 500 || response.StatusCode >= 600) {
			return last
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return last
}

func (c *CommitteeClient) get(ctx context.Context, path string, limit int) ([]byte, error) {
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if e != nil {
		return nil, e
	}
	response, e := c.HTTP.Do(request)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("committee query HTTP %d", response.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, int64(limit+1)))
	if e != nil {
		return nil, e
	}
	if len(raw) > limit {
		return nil, protocol.ErrEncoding
	}
	return raw, nil
}
func (c *CommitteeClient) Receipt(ctx context.Context, kind protocol.FactKind, key protocol.Hash) (finality.FactProof, error) {
	raw, e := c.get(ctx, fmt.Sprintf("/v1/receipts/%d/%s", kind, key.String()), finality.MaxProofBytes)
	if e != nil {
		return finality.FactProof{}, e
	}
	return finality.Decode(raw)
}
func (c *CommitteeClient) Certificate(ctx context.Context, id protocol.SpendFactID) (protocol.TXCer, error) {
	raw, e := c.get(ctx, "/v1/txcers/"+protocol.Hash(id).String(), protocol.MaxCertificateBytes)
	if e != nil {
		return protocol.TXCer{}, e
	}
	return protocol.DecodeCertificate(raw)
}
