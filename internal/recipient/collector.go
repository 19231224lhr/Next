// Package recipient provides an opt-in receiving-wallet collector. The payer's
// signed request goes to the recipient, which collects the existing member votes.
package recipient

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
	"utxo/internal/gateway"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type Collector struct {
	collector *gateway.Collector
	wallet    *wallet.Wallet
	owner     protocol.PublicKey
	org       protocol.OrgConfig
	db        store.Store
	slots     chan struct{}
	lifecycle sync.RWMutex
	closed    bool
}

// New uses the same durable store as the supplied gateway collector. Its public
// relay must consume that collector's outbox, as with a regular gateway. Only
// outputs owned by this wallet are acknowledged as received; payer change and
// other recipients remain available in the complete returned certificate.
func New(c *gateway.Collector, w *wallet.Wallet, owner protocol.PublicKey, org protocol.OrgConfig, db store.Store) (*Collector, error) {
	if c == nil || w == nil || db == nil || owner == (protocol.PublicKey{}) || org.Validate() != nil {
		return nil, protocol.ErrRule
	}
	return &Collector{collector: c, wallet: w, owner: owner, org: org, db: db, slots: make(chan struct{}, 16)}, nil
}

// Payment exposes retained full material for restart, retry, and public relay.
func (c *Collector) Payment(id protocol.TxID) (protocol.DirectPayment, error) {
	var raw []byte
	e := c.db.View(func(v state.ReadView) error {
		var e error
		raw, e = v.Get(state.Key(state.KeyCollected, id[:]))
		return e
	})
	if e != nil {
		return protocol.DirectPayment{}, e
	}
	p, e := protocol.DecodeDirectPayment(raw)
	if e != nil {
		return p, e
	}
	if p.Tx.ID() != id || p.Certificate.Summary.Tx != id || p.Certificate.Verify(c.org) != nil {
		return protocol.DirectPayment{}, protocol.ErrAuth
	}
	return p, nil
}

func (c *Collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.serve(w, r, false)
}

// PrepareHTTP reserves the local received coin for an owner-signed onward
// request. A 204 means saved locally, not approved by members. The caller then
// submits the same bytes to the next recipient collector or regular gateway.
func (c *Collector) PrepareHTTP(w http.ResponseWriter, r *http.Request) { c.serve(w, r, true) }

func (c *Collector) serve(w http.ResponseWriter, r *http.Request, prepare bool) {
	c.lifecycle.RLock()
	defer c.lifecycle.RUnlock()
	if c.closed {
		http.Error(w, "SHUTTING_DOWN", 503)
		return
	}
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != transport.MediaType {
		http.Error(w, "INVALID_ENCODING", 400)
		return
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		http.Error(w, "LIMITED", 429)
		return
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, protocol.MaxRequestBytes))
	if e != nil {
		http.Error(w, "INVALID_ENCODING", 400)
		return
	}
	req, e := protocol.DecodeDirectRequest(raw)
	if e != nil {
		http.Error(w, "INVALID_REQUEST", 400)
		return
	}
	if prepare {
		if e = c.wallet.SaveDirectRequest(req); e != nil {
			http.Error(w, "INVALID_OWNER_OR_INPUT", 400)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var indices []uint32
	for i, o := range req.Tx.Body.Outputs {
		if o.Recipient.Owner == c.owner {
			indices = append(indices, uint32(i))
		}
	}
	if len(indices) == 0 {
		http.Error(w, "WRONG_RECIPIENT", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	cert, e := c.collector.CollectDirect(ctx, req)
	if e != nil {
		http.Error(w, "QUORUM_UNAVAILABLE_RETRY_SAME_REQUEST", 503)
		return
	}
	p := protocol.DirectPayment{Tx: req.Tx, Certificate: cert, InputCertificates: req.InputCertificates}
	// Save full material and the public outbox before acknowledging a received coin.
	if e = c.collector.PersistDirect(p); e != nil {
		http.Error(w, "PERSIST_FAILED_RETRY_SAME_REQUEST", 503)
		return
	}
	for _, i := range indices {
		if e = c.wallet.ReceiveDirect(req.Tx.Body.Outputs[i], cert, i); e != nil {
			http.Error(w, "RECEIVE_FAILED_RETRY_SAME_REQUEST", 503)
			return
		}
	}
	raw, e = cert.MarshalBinary()
	if e != nil {
		http.Error(w, "INVALID_CERTIFICATE", 500)
		return
	}
	w.Header().Set("Content-Type", transport.MediaType)
	_, _ = w.Write(raw)
}

// Close rejects new requests and joins active handlers before their stores close.
func (c *Collector) Close() { c.lifecycle.Lock(); defer c.lifecycle.Unlock(); c.closed = true }
