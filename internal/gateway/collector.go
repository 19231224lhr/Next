package gateway

import (
	"context"
	"errors"
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
			if len(cert.QC.Votes) == 3 {
				if e = cert.Verify(c.org); e != nil {
					return protocol.TXCer{}, e
				}
				raw, e := cert.MarshalBinary()
				if e != nil {
					return protocol.TXCer{}, e
				}
				e = c.db.Update(func(v state.ReadView) ([]state.Change, error) {
					o := state.NewOverlay(v)
					// First durable complete certificate wins; alternative valid subsets have
					// the same fact identity and cannot create a new settlement action.
					if old, e := v.Get(key); e == nil {
						saved = old
						return nil, nil
					} else if !errors.Is(e, state.ErrNotFound) {
						return nil, e
					}
					o.Set(key, raw)
					if e := state.Put(o, state.Key(state.KeyOutbox, cert.QC.Fact[:]), state.Outbox{Fact: cert.QC.Fact, Certificate: raw, Origin: c.org.Org}); e != nil {
						return nil, e
					}
					return o.Changes(), nil
				})
				if e != nil {
					return protocol.TXCer{}, e
				}
				if saved != nil {
					return protocol.DecodeCertificate(saved)
				}
				return *cert, nil
			}
		}
	}
	return protocol.TXCer{}, errors.New("valid quorum unavailable")
}
