package gateway

import (
	"context"
	"errors"
	"fmt"
	"utxo/internal/requesttrace"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type MemberClient interface {
	Approve(context.Context, protocol.PaymentRequest) (protocol.Approval, error)
	Install(context.Context, protocol.TXCer) error
}
type Collector struct {
	org     protocol.OrgConfig
	members [4]MemberClient
	db      store.Store
}

func New(org protocol.OrgConfig, members [4]MemberClient, db store.Store) (*Collector, error) {
	if org.Validate() != nil || db == nil {
		return nil, protocol.ErrRule
	}
	for _, m := range members {
		if m == nil {
			return nil, protocol.ErrRule
		}
	}
	return &Collector{org: org, members: members, db: db}, nil
}

type answer struct {
	index    uint16
	approval protocol.Approval
	err      error
}

func (c *Collector) Collect(ctx context.Context, request protocol.PaymentRequest) (protocol.TXCer, error) {
	if e := request.Tx.VerifyAuth(); e != nil {
		return protocol.TXCer{}, e
	}
	if request.Tx.Body.Config != c.org.Hash() {
		return protocol.TXCer{}, protocol.ErrAuth
	}
	id := request.Tx.Body.ID()
	key := state.Key(state.KeyCollected, id[:])
	var saved []byte
	e := c.db.View(func(v state.ReadView) error {
		raw, e := v.Get(key)
		if errors.Is(e, state.ErrNotFound) {
			return nil
		}
		saved = raw
		return e
	})
	if e != nil {
		return protocol.TXCer{}, e
	}
	if saved != nil {
		return protocol.DecodeCertificate(saved)
	}
	requesttrace.Mark(ctx, "fanout_start")
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	responses := make(chan answer, 4)
	for i, m := range c.members {
		go func(i int, m MemberClient) { a, e := m.Approve(run, request); responses <- answer{uint16(i), a, e} }(i, m)
	}
	groups := make(map[protocol.SpendFactID]*protocol.TXCer)
	for received := 0; received < 4; received++ {
		select {
		case <-ctx.Done():
			return protocol.TXCer{}, ctx.Err()
		case result := <-responses:
			a := result.approval
			if result.err != nil || a.Vote.Member != result.index || a.Verify(request.Tx, c.org) != nil {
				continue
			}
			cert, found := groups[a.Fact]
			if !found {
				cert = &protocol.TXCer{Tx: request.Tx, Admission: a.Admission, Effects: a.Effects, QC: protocol.SpendQC{Fact: a.Fact}}
				groups[a.Fact] = cert
			}
			cert.QC.Votes = append(cert.QC.Votes, a.Vote)
			requesttrace.Mark(ctx, fmt.Sprintf("vote_%d_accepted", result.index))
			if len(cert.QC.Votes) == 3 {
				requesttrace.Mark(ctx, "quorum_collected")
				if e = cert.Verify(c.org); e != nil {
					return protocol.TXCer{}, e
				}
				requesttrace.Mark(ctx, "certificate_verified")
				return *cert, nil
			}
		}
	}
	return protocol.TXCer{}, errors.New("valid quorum unavailable")
}

// Persist is a background delivery aid, not a prerequisite for a valid certificate.
// The HTTP caller sends the complete response before entering this synchronous write.
func (c *Collector) Persist(cert protocol.TXCer) error {
	if err := cert.Verify(c.org); err != nil {
		return err
	}
	raw, err := cert.MarshalBinary()
	if err != nil {
		return err
	}
	id := cert.Tx.Body.ID()
	key := state.Key(state.KeyCollected, id[:])
	return c.db.Update(func(v state.ReadView) ([]state.Change, error) {
		if _, err := v.Get(key); err == nil {
			return nil, nil
		} else if !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
		o := state.NewOverlay(v)
		o.Set(key, raw)
		if err := state.Put(o, state.Key(state.KeyOutbox, cert.QC.Fact[:]), state.Outbox{Fact: cert.QC.Fact, Certificate: raw, Origin: c.org.Org}); err != nil {
			return nil, err
		}
		return o.Changes(), nil
	})
}
