package gateway

import (
	"bytes"
	"context"
	"errors"
	"utxo/internal/state"
	"utxo/protocol"
)

var ErrSigningBusy = errors.New("another signing request must finish first")

type directSigningGate struct {
	slot chan struct{}
	head protocol.Hash
}

// Completed results precede the gate: a lost response must not become trapped
// behind a later head that is waiting for this payment's public settlement.
func (c *Collector) directResult(request protocol.DirectRequest, raw []byte) (protocol.OutputCertificate, bool, error) {
	id := request.Tx.ID()
	var saved []byte
	err := c.db.View(func(v state.ReadView) error {
		var e error
		saved, e = v.Get(state.Key(state.KeyCollected, id[:]))
		return e
	})
	if errors.Is(err, state.ErrNotFound) {
		return protocol.OutputCertificate{}, false, nil
	}
	if err != nil {
		return protocol.OutputCertificate{}, false, err
	}
	p, err := protocol.DecodeDirectPayment(saved)
	if err != nil {
		return protocol.OutputCertificate{}, false, err
	}
	expected, err := (protocol.DirectRequest{Tx: p.Tx, InputCertificates: p.InputCertificates}).MarshalBinary()
	if err != nil || !bytes.Equal(raw, expected) || p.Certificate.Summary.Tx != id || p.Certificate.Verify(c.org) != nil {
		return protocol.OutputCertificate{}, false, protocol.ErrAuth
	}
	return p.Certificate, true, nil
}

// EnableSerialDirect enables opt-in admission for a fresh single-collector lab.
// Call before serving requests. A failed/cancelled head remains pinned until
// that exact request obtains a valid quorum. No member lock or budget is freed.
// ponytail: one head prevents fragmented approvals; no failover/abandonment
// recovery is promised, and a permanently invalid head stops this lab mode.
func (c *Collector) EnableSerialDirect() {
	c.directGate = &directSigningGate{slot: make(chan struct{}, 1)}
}

func (g *directSigningGate) enter(ctx context.Context, raw []byte) error {
	select {
	case g.slot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-g.slot
		return err
	}
	key := protocol.Digest("DIRECT_SIGNING_HEAD", raw)
	if g.head != (protocol.Hash{}) && g.head != key {
		<-g.slot
		return ErrSigningBusy
	}
	g.head = key
	return nil
}

func (g *directSigningGate) leave(complete bool) {
	if complete {
		g.head = protocol.Hash{}
	}
	<-g.slot
}
