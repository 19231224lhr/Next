package gateway

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"utxo/protocol"
)

type encodedDirectClient struct {
	MemberClient
	call func(context.Context, []byte) (protocol.DirectApproval, error)
}

func (c encodedDirectClient) ApproveDirect(context.Context, protocol.DirectRequest) (protocol.DirectApproval, error) {
	return protocol.DirectApproval{}, protocol.ErrUnsupported
}
func (c encodedDirectClient) InstallDirect(context.Context, protocol.DirectPayment) error {
	return protocol.ErrUnsupported
}
func (c encodedDirectClient) ApproveDirectBytes(ctx context.Context, raw []byte) (protocol.DirectApproval, error) {
	return c.call(ctx, raw)
}

func TestCollectDirectSharesEncodedRequest(t *testing.T) {
	c, _, payments := directRelayFixture(t, 1)
	p := payments[0]
	req := protocol.DirectRequest{Tx: p.Tx}
	expected, err := req.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var buffers [][]byte
	for i := range c.members {
		c.members[i] = encodedDirectClient{call: func(ctx context.Context, raw []byte) (protocol.DirectApproval, error) {
			mu.Lock()
			buffers = append(buffers, raw)
			mu.Unlock()
			if i >= len(p.Certificate.QC.Votes) {
				return protocol.DirectApproval{}, protocol.ErrUnsupported
			}
			return protocol.DirectApproval{Summary: p.Certificate.Summary, Vote: p.Certificate.QC.Votes[i]}, nil
		}}
	}
	cert, err := c.CollectDirect(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Verify(c.org) != nil {
		t.Fatal("invalid returned quorum")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(buffers) < 3 {
		t.Fatalf("only %d encoded calls", len(buffers))
	}
	for _, raw := range buffers {
		if !bytes.Equal(raw, expected) || &raw[0] != &buffers[0][0] {
			t.Fatal("fanout did not share canonical immutable bytes")
		}
	}
}

func TestCollectDirectInvalidAuthNeverFansOut(t *testing.T) {
	c, _, payments := directRelayFixture(t, 1)
	p := payments[0]
	p.Tx.Auth[0].Signature[0] ^= 1
	for i := range c.members {
		c.members[i] = encodedDirectClient{call: func(context.Context, []byte) (protocol.DirectApproval, error) {
			t.Error("invalid auth reached member transport")
			return protocol.DirectApproval{}, protocol.ErrAuth
		}}
	}
	if _, err := c.CollectDirect(context.Background(), protocol.DirectRequest{Tx: p.Tx}); err == nil {
		t.Fatal("invalid auth accepted")
	}
}
