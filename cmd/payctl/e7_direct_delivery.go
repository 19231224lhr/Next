package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"utxo/internal/delivery"
	"utxo/internal/wallet"
	"utxo/protocol"
)

// This optional laboratory metadata drives continuation, never authorization.
type e7DirectMetadata struct {
	Phase                                                  string
	Index, Lane, Generation, Hop, Sender, Receiver, Parent int
	Tx                                                     protocol.TxID
}

func (m e7DirectMetadata) header() (string, error) {
	b, e := json.Marshal(m)
	return base64.RawURLEncoding.EncodeToString(b), e
}

type e7DeliveryTransport struct {
	base     http.RoundTripper
	metadata string
}

func (t e7DeliveryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(delivery.MetadataHeader, t.metadata)
	return t.base.RoundTrip(r)
}
func e7DeliveryClient(c *http.Client, metadata string) *http.Client {
	copy := *c
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	copy.Transport = e7DeliveryTransport{base, metadata}
	return &copy
}

func (w *e7Wallet) directReceiver() http.Handler {
	locals := map[protocol.PublicKey]*wallet.Wallet{}
	for i, local := range w.wallets {
		if local != nil {
			locals[w.descriptors[i].Owner] = local
		}
	}
	return delivery.Handler(locals, func(p protocol.DirectPayment, index uint32, metadata string) error {
		if metadata == "" {
			return nil
		}
		var m e7DirectMetadata
		b, e := base64.RawURLEncoding.DecodeString(metadata)
		if e != nil {
			return e
		}
		if e = json.Unmarshal(b, &m); e != nil {
			return e
		}
		if index != 0 {
			return nil
		} // Change outputs are persisted but do not advance the laboratory chain.
		if m.Tx != p.Tx.ID() || m.Receiver < 0 || m.Receiver >= len(w.descriptors) || w.descriptors[m.Receiver].Owner != p.Tx.Body.Outputs[index].Recipient.Owner {
			return protocol.ErrAuth
		}
		d := e7Delivery{Phase: m.Phase, Index: m.Index, Lane: m.Lane, Generation: m.Generation, Hop: m.Hop, Sender: m.Sender, Receiver: m.Receiver, Parent: m.Parent, Output: p.Tx.Body.Outputs[index]}
		_, e = w.acceptDelivery(d, p.Certificate, false)
		return e
	})
}

func (w *e7Wallet) directAck(rw http.ResponseWriter, r *http.Request) {
	index, e := strconv.Atoi(r.URL.Query().Get("index"))
	if e != nil {
		http.Error(rw, "invalid index", 400)
		return
	}
	w.receipts.mu.Lock()
	f := w.receipts.entries[index]
	w.receipts.mu.Unlock()
	if f == nil {
		rw.WriteHeader(202)
		return
	}
	select {
	case <-f.done:
		if f.err != nil {
			http.Error(rw, f.err.Error(), 400)
			return
		}
		json.NewEncoder(rw).Encode(f.ack)
	default:
		rw.WriteHeader(202)
	}
}
func e7WaitDirectAck(ctx context.Context, c *http.Client, url string, index int, fact protocol.SpendFactID) (e7Ack, error) {
	for {
		req, e := http.NewRequestWithContext(ctx, "GET", url+"/v3/receive/ack?index="+strconv.Itoa(index), nil)
		if e != nil {
			return e7Ack{}, e
		}
		resp, e := c.Do(req)
		if e != nil {
			return e7Ack{}, e
		}
		if resp.StatusCode == 200 {
			var a e7Ack
			e = json.NewDecoder(resp.Body).Decode(&a)
			resp.Body.Close()
			if e == nil && (a.Index != index || a.Fact != fact) {
				e = fmt.Errorf("direct receipt identity mismatch")
			}
			return a, e
		}
		resp.Body.Close()
		if resp.StatusCode != 202 {
			return e7Ack{}, fmt.Errorf("direct receipt HTTP %d", resp.StatusCode)
		}
		if e = budgetPause(ctx, 2*time.Millisecond); e != nil {
			return e7Ack{}, e
		}
	}
}
