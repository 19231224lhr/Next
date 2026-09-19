package gateway

import (
	"context"
	"crypto/ed25519"
	"errors"
	"time"
	"utxo/internal/requesttrace"
	"utxo/internal/state"
	"utxo/protocol"
)

type DirectMemberClient interface {
	ApproveDirect(context.Context, protocol.DirectRequest) (protocol.DirectApproval, error)
	InstallDirect(context.Context, protocol.DirectPayment) error
}

func (c *Collector) CollectDirect(ctx context.Context, request protocol.DirectRequest) (protocol.OutputCertificate, error) {
	if err := request.Tx.VerifyAuth(); err != nil {
		return protocol.OutputCertificate{}, err
	}
	if request.Tx.Body.Config != c.org.Hash() {
		return protocol.OutputCertificate{}, protocol.ErrAuth
	}
	type result struct {
		index    int
		approval protocol.DirectApproval
		err      error
	}
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	responses := make(chan result, 4)
	for i, m := range c.members {
		go func(i int, m MemberClient) {
			client, ok := m.(DirectMemberClient)
			if !ok {
				responses <- result{err: protocol.ErrUnsupported}
				return
			}
			a, err := client.ApproveDirect(run, request)
			responses <- result{index: i, approval: a, err: err}
		}(i, m)
	}
	groups := map[protocol.SpendFactID]*protocol.OutputCertificate{}
	failures := []error{errors.New("valid quorum unavailable")}
	for n := 0; n < 4; n++ {
		select {
		case <-ctx.Done():
			return protocol.OutputCertificate{}, ctx.Err()
		case r := <-responses:
			if r.err != nil {
				failures = append(failures, r.err)
				continue
			}
			if int(r.approval.Vote.Member) != r.index {
				continue
			}
			s := r.approval.Summary
			fact := s.Fact()
			signed := protocol.Digest("SPEND_VOTE", fact[:])
			if s.Fact() != protocol.SummaryFor(request.Tx, s.Admission).Fact() || s.Validate() != nil || !ed25519.Verify(c.org.Members[r.index][:], signed[:], r.approval.Vote.Signature[:]) {
				continue
			}
			cert := groups[fact]
			if cert == nil {
				cert = &protocol.OutputCertificate{Summary: s, QC: protocol.SpendQC{Fact: fact}}
				groups[fact] = cert
			}
			cert.QC.Votes = append(cert.QC.Votes, r.approval.Vote)
			if len(cert.QC.Votes) == 3 {
				return *cert, cert.Verify(c.org)
			}
		}
	}
	return protocol.OutputCertificate{}, errors.Join(failures...)
}

func (c *Collector) PersistDirect(p protocol.DirectPayment) error {
	if p.Certificate.Verify(c.org) != nil || p.Certificate.Summary.Tx != p.Tx.ID() {
		return protocol.ErrAuth
	}
	raw, err := p.MarshalBinary()
	if err != nil {
		return err
	}
	fact := p.Certificate.QC.Fact
	id := p.Tx.ID()
	return c.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		key := state.Key(state.KeyCollected, id[:])
		if _, err := o.Get(key); err == nil {
			return nil, nil
		} else if !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
		o.Set(key, raw)
		if _, found, err := state.Load[bool](o, state.Key(state.KeyObserved, fact[:])); err != nil {
			return nil, err
		} else if found {
			return o.Changes(), nil
		}
		if err := state.Put(o, state.Key(state.KeyOutbox, fact[:]), state.Outbox{Fact: fact, Certificate: raw, Origin: p.Tx.Body.Certifier}); err != nil {
			return nil, err
		}
		return o.Changes(), nil
	})
}

func (r *Relay) deliverDirect(ctx context.Context, key []byte, pending state.Outbox) error {
	requesttrace.Payment("relay_enter", pending.Fact)
	payment, err := protocol.DecodeDirectPayment(pending.Certificate)
	if err != nil {
		return err
	}
	c := payment.Certificate
	org, ok := r.Organizations[c.Summary.Config]
	if !ok || c.Verify(org) != nil {
		return protocol.ErrAuth
	}
	if pending.PublicComplete {
		return nil
	}
	if !r.MemberRelay {
		requesttrace.Payment("install_fanout_start", pending.Fact)
		targets := r.installTargets(pending.Fact)
		done := make(chan struct{}, 4)
		for i, m := range r.Members[c.Summary.Issuer] {
			if targets&(1<<i) == 0 {
				continue
			}
			go func(i int, m MemberClient) {
				if client, ok := m.(DirectMemberClient); ok && client.InstallDirect(ctx, payment) == nil {
					r.installAck(pending.Fact, i)
				}
				done <- struct{}{}
			}(i, m)
		}
		for i := 0; i < 4; i++ {
			if targets&(1<<i) != 0 {
				<-done
			}
		}
	}
	requesttrace.Payment("relay_ready", pending.Fact)
	now := time.Now().UnixNano()
	if now >= pending.NextSubmitUnixNS {
		// No random outer envelope: retries preserve the redaction-aware identity.
		requesttrace.Payment("submit_start", pending.Fact)
		_ = r.Public.Submit(ctx, pending.Certificate)
		requesttrace.Payment("submit_done", pending.Fact)
		pending.NextSubmitUnixNS = now + int64(2*time.Second)
	}
	return r.DB.Update(func(v state.ReadView) ([]state.Change, error) {
		if _, err := v.Get(key); errors.Is(err, state.ErrNotFound) {
			r.forgetInstall(pending.Fact)
			return nil, nil
		} else if err != nil {
			return nil, err
		}
		o := state.NewOverlay(v)
		if err := state.Put(o, key, pending); err != nil {
			return nil, err
		}
		return o.Changes(), nil
	})
}
