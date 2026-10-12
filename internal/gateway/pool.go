package gateway

import (
	"context"
	"crypto/ed25519"
	"errors"
	"time"
	"utxo/protocol"
)

type PoolApprover interface {
	ApprovePool(context.Context, protocol.PoolRequest) (protocol.PoolVote, error)
	InstallPool(context.Context, protocol.PoolCertificate) error
	PoolCertificate(context.Context, protocol.Hash) (protocol.PoolCertificate, error)
}

// CollectPool only transports approvals; it creates no public redemption right
// for transfer receipts. Each member independently enforces its persisted lock.
func CollectPool(ctx context.Context, org protocol.OrgConfig, members [4]PoolApprover, r protocol.PoolRequest) (protocol.PoolCertificate, error) {
	c, err := CollectPoolCertificate(ctx, org, members, r)
	if err != nil {
		return c, err
	}
	return c, installPoolQuorum(ctx, members, c)
}

// CollectPoolCertificate obtains the full QC for a receiver that will overlap
// durable receipt storage with member installation before reporting completion.
func CollectPoolCertificate(ctx context.Context, org protocol.OrgConfig, members [4]PoolApprover, r protocol.PoolRequest) (protocol.PoolCertificate, error) {
	c := protocol.PoolCertificate{Action: r.Action}
	if e := r.Action.Verify(org); e != nil {
		return c, e
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		vote  protocol.PoolVote
		err   error
		index uint16
	}
	ch := make(chan result, 4)
	id := r.Action.ID()
	for i, m := range members {
		go func(i int, m PoolApprover) {
			if m == nil {
				ch <- result{err: protocol.ErrAuth}
				return
			}
			v, e := m.ApprovePool(ctx, r)
			ch <- result{v, e, uint16(i)}
		}(i, m)
	}
	h := protocol.Digest("INTERNAL_POOL_VOTE_V1", id[:])
	var failures []error
	votesRemaining, recoveriesRemaining := 4, 0
	var recovered chan protocol.PoolCertificate
	startRecovery := func() {
		if recovered != nil {
			return
		}
		recovered = make(chan protocol.PoolCertificate, 4)
		recoveriesRemaining = 4
		for _, m := range members {
			go func(m PoolApprover) {
				var saved protocol.PoolCertificate
				if m != nil {
					candidate, err := m.PoolCertificate(ctx, id)
					if err == nil && candidate.Action.ID() == id && candidate.Verify(org) == nil {
						saved = candidate
					}
				}
				recovered <- saved
			}(m)
		}
	}
	// A stalled approval must not hold a previously installed QC hostage.
	slow := time.NewTimer(200 * time.Millisecond)
	defer slow.Stop()
	for votesRemaining > 0 || recoveriesRemaining > 0 {
		select {
		case <-ctx.Done():
			return c, ctx.Err()
		case <-slow.C:
			startRecovery()
		case saved := <-recovered:
			recoveriesRemaining--
			if len(saved.Votes) >= 3 {
				return saved, nil
			}
		case res := <-ch:
			votesRemaining--
			if res.err != nil {
				failures = append(failures, res.err)
				startRecovery()
				continue
			}
			v := res.vote
			if v.Member != res.index || v.Member >= 4 || !ed25519.Verify(org.Members[v.Member][:], h[:], v.Signature[:]) {
				failures = append(failures, protocol.ErrAuth)
				startRecovery()
				continue
			}
			c.Votes = append(c.Votes, v)
			if len(c.Votes) == 3 {
				if err := c.Verify(org); err != nil {
					return c, err
				}
				return c, nil
			}
		}
	}
	return c, errors.Join(append([]error{errors.New("pool quorum unavailable; retry the same signed request")}, failures...)...)
}

// Successful collection leaves two acknowledged copies on distinct configured
// members, hence at least one honest copy for f=1. A crash before this phase
// completes can still strand a partially collected QC; no timeout unlock exists.
func installPoolQuorum(ctx context.Context, members [4]PoolApprover, c protocol.PoolCertificate) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 4)
	for _, m := range members {
		go func(m PoolApprover) {
			if m == nil {
				results <- protocol.ErrAuth
				return
			}
			results <- m.InstallPool(ctx, c)
		}(m)
	}
	acks := 0
	var failures []error
	for range members {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-results:
			if err != nil {
				failures = append(failures, err)
				continue
			}
			acks++
			if acks == 2 {
				return nil
			}
		}
	}
	return errors.Join(append([]error{errors.New("pool certificate lacks two durable copies; retry the same signed request")}, failures...)...)
}
