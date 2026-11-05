package wallet

import (
	"context"
	"errors"
	"utxo/internal/requesttrace"
	"utxo/protocol"
)

type DirectApprover interface {
	ApproveDirect(context.Context, protocol.DirectRequest) (protocol.DirectApproval, error)
}

// CollectDirect runs in the wallet. No collector request, INSTALL, public
// execution, or fourth-member response is required before returning a TXCer.
func CollectDirect(ctx context.Context, org protocol.OrgConfig, members [4]DirectApprover, request protocol.DirectRequest) (protocol.OutputCertificate, error) {
	if org.Validate() != nil || request.Tx.Body.Config != org.Hash() {
		return protocol.OutputCertificate{}, protocol.ErrAuth
	}
	full, compact, signers, err := request.MarshalForContinuation()
	if err != nil {
		return protocol.OutputCertificate{}, err
	}
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	type response struct {
		index    int
		approval protocol.DirectApproval
		err      error
	}
	responses := make(chan response, 4)
	for i, m := range members {
		go func(i int, m DirectApprover) {
			var a protocol.DirectApproval
			var err error
			if m == nil {
				responses <- response{i, a, protocol.ErrUnsupported}
				return
			}
			if prepared, ok := m.(interface {
				ApproveDirectPrepared(context.Context, protocol.FastTx, []byte, []byte) (protocol.DirectApproval, error)
			}); ok {
				var abbreviated []byte
				if signers&(1<<i) != 0 {
					abbreviated = compact
				}
				a, err = prepared.ApproveDirectPrepared(run, request.Tx, full, abbreviated)
			} else if encoded, ok := m.(interface {
				ApproveDirectBytes(context.Context, []byte) (protocol.DirectApproval, error)
			}); ok {
				a, err = encoded.ApproveDirectBytes(run, full)
			} else {
				a, err = m.ApproveDirect(run, request)
			}
			responses <- response{i, a, err}
		}(i, m)
	}
	groups := make(map[protocol.SpendFactID]*protocol.OutputCertificate)
	failures := []error{errors.New("valid GM quorum unavailable")}
	for range members {
		select {
		case <-ctx.Done():
			return protocol.OutputCertificate{}, ctx.Err()
		case r := <-responses:
			if r.err != nil {
				failures = append(failures, r.err)
				continue
			}
			if err := protocol.ValidateDirectApproval(org, request, r.approval, r.index); err != nil {
				failures = append(failures, err)
				continue
			}
			fact := r.approval.Summary.Fact()
			c := groups[fact]
			if c == nil {
				c = &protocol.OutputCertificate{Summary: r.approval.Summary, QC: protocol.SpendQC{Fact: fact}}
				groups[fact] = c
			}
			c.QC.Votes = append(c.QC.Votes, r.approval.Vote)
			if len(c.QC.Votes) == 3 {
				if err := c.Verify(org); err != nil {
					return protocol.OutputCertificate{}, err
				}
				requesttrace.Mark(ctx, "wallet_certificate_formed")
				return *c, nil
			}
		}
	}
	return protocol.OutputCertificate{}, errors.Join(failures...)
}
