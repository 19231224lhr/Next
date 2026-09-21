package gateway

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strconv"
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
	requesttrace.Mark(ctx, "collect_enter")
	raw, err := request.MarshalBinary()
	if err != nil {
		return protocol.OutputCertificate{}, err
	}
	requesttrace.Mark(ctx, "owner_verified")
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
	requesttrace.Mark(ctx, "fanout_ready")
	for i, m := range c.members {
		node := ""
		if requesttrace.Enabled(ctx) {
			node = "member-" + strconv.Itoa(i)
			requesttrace.MarkNode(ctx, node, "dispatch_ready")
		}
		go func(i int, m MemberClient) {
			requesttrace.MarkNode(run, node, "dispatch_started")
			client, ok := m.(DirectMemberClient)
			if !ok {
				responses <- result{err: protocol.ErrUnsupported}
				return
			}
			var a protocol.DirectApproval
			var err error
			// HTTP peers share one immutable encoding; in-process peers retain
			// their typed interface. Each receiver still validates independently.
			if encoded, ok := client.(interface {
				ApproveDirectBytes(context.Context, []byte) (protocol.DirectApproval, error)
			}); ok {
				a, err = encoded.ApproveDirectBytes(run, raw)
			} else {
				a, err = client.ApproveDirect(run, request)
			}
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
			if requesttrace.Enabled(ctx) {
				requesttrace.MarkNode(ctx, "member-"+strconv.Itoa(r.index), "vote_selected")
			}
			if len(cert.QC.Votes) == 3 {
				requesttrace.Mark(ctx, "quorum_collected")
				err := cert.Verify(c.org)
				requesttrace.Mark(ctx, "certificate_verified")
				return *cert, err
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
	requesttrace.Payment("persist_update_requested", fact)
	err = c.db.Update(func(v state.ReadView) ([]state.Change, error) {
		requesttrace.Payment("persist_callback", fact)
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
	if err == nil && c.NotifyPersisted != nil {
		c.NotifyPersisted(fact)
	}
	return err
}

func (r *Relay) deliverDirect(ctx context.Context, key []byte, pending state.Outbox) error {
	// Member fallback waits outside the decoding, verification and write path.
	if r.MemberRelay && (pending.PublicComplete || time.Now().UnixNano() < pending.NextSubmitUnixNS) {
		return nil
	}
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
	return r.submitDirect(ctx, key, pending, payment)
}

func (r *Relay) submitDirect(ctx context.Context, key []byte, pending state.Outbox, payment protocol.DirectPayment) error {
	now := time.Now().UnixNano()
	if now >= pending.NextSubmitUnixNS {
		raw, err := payment.Submission().MarshalBinary()
		if err != nil {
			return err
		}
		// No random outer envelope: retries preserve the redaction-aware identity.
		requesttrace.Payment("submit_start", pending.Fact)
		_ = r.Public.Submit(ctx, raw)
		requesttrace.Payment("submit_done", pending.Fact)
		pending.NextSubmitUnixNS = now + int64(2*time.Second)
	}
	requesttrace.Payment("relay_update_requested", pending.Fact)
	err := r.DB.Update(func(v state.ReadView) ([]state.Change, error) {
		requesttrace.Payment("relay_callback", pending.Fact)
		current, found, err := state.Load[state.Outbox](v, key)
		if err != nil {
			return nil, err
		}
		if !found {
			r.forgetInstall(pending.Fact)
			return nil, nil
		}
		current.NextSubmitUnixNS = max(current.NextSubmitUnixNS, pending.NextSubmitUnixNS)
		o := state.NewOverlay(v)
		if err := state.Put(o, key, current); err != nil {
			return nil, err
		}
		return o.Changes(), nil
	})
	requesttrace.Payment("relay_update_returned", pending.Fact)
	return err
}
