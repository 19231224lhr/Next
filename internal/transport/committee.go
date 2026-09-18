package transport

import (
	"bytes"
	"context"
	"crypto/rand"
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
	BaseURL string
	HTTP    *http.Client
}

func NewCommitteeClient(url string) *CommitteeClient {
	return &CommitteeClient{BaseURL: strings.TrimRight(url, "/"), HTTP: &http.Client{Timeout: 5 * time.Second}}
}
func (c *CommitteeClient) Submit(ctx context.Context, raw []byte) error {
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

	request, e := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/commands", bytes.NewReader(raw))
	if e != nil {
		return e
	}
	request.Header.Set("Content-Type", MediaType)
	if requesttrace.Settlement != nil {
		request.Header.Set(requesttrace.DeliverySourceHeader, filepath.Base(os.Args[0]))
		request.Header.Set(requesttrace.DeliveryTimeHeader, strconv.FormatInt(time.Now().UnixNano(), 10))
	}
	response, e := c.HTTP.Do(request)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("committee submission HTTP %d", response.StatusCode)
	}
	return nil
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
