// Package delivery delivers complete direct payments independently of sender responses.
// Its dedicated durable database is an at-least-once outbox, not a public relay.
package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

const MetadataHeader = "X-Next-Delivery-Metadata"
const MaxMetadata = 2048
const workers = 4

type Route struct {
	Owner protocol.PublicKey
	URL   string
}
type record struct {
	Payment  []byte
	Index    uint32
	Metadata string
}
type Outbox struct {
	db     store.Store
	routes map[protocol.PublicKey]string
	orgs   map[protocol.Hash]protocol.OrgConfig
	wake   chan struct{}
	client *http.Client
}

func New(db store.Store, routes []Route, orgs []protocol.OrgConfig) (*Outbox, error) {
	return NewWithClient(db, routes, orgs, &http.Client{Timeout: 2 * time.Second})
}

// NewWithClient accepts a configured TLS transport without allowing redirects
// away from the operator's recipient endpoint. Configure before calling Run.
func NewWithClient(db store.Store, routes []Route, orgs []protocol.OrgConfig, client *http.Client) (*Outbox, error) {
	if db == nil || client == nil {
		return nil, protocol.ErrRule
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if copyClient.Timeout == 0 {
		copyClient.Timeout = 2 * time.Second
	}
	q := &Outbox{db: db, routes: map[protocol.PublicKey]string{}, orgs: map[protocol.Hash]protocol.OrgConfig{}, wake: make(chan struct{}, 1), client: &copyClient}
	for _, r := range routes {
		u, e := url.Parse(r.URL)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || r.Owner == (protocol.PublicKey{}) {
			return nil, errors.New("invalid operator delivery route")
		}
		if _, ok := q.routes[r.Owner]; ok {
			return nil, errors.New("duplicate delivery owner")
		}
		q.routes[r.Owner] = r.URL
	}
	for _, o := range orgs {
		q.orgs[o.Hash()] = o
	}
	return q, nil
}

// CheckRoutes is called before quorum collection; callback addresses never come from senders.
func (q *Outbox) CheckRoutes(tx protocol.FastTx) error {
	for _, o := range tx.Body.Outputs {
		if _, ok := q.routes[o.Recipient.Owner]; !ok {
			return errors.New("recipient delivery route missing")
		}
	}
	return nil
}

// Enqueue commits before returning. Metadata is optional bounded experiment context,
// never trusted for routing or authorization. Completion tombstones prevent reopening.
func (q *Outbox) Enqueue(p protocol.DirectPayment, metadata string) error {
	if len(metadata) > MaxMetadata {
		return protocol.ErrEncoding
	}
	if e := q.CheckRoutes(p.Tx); e != nil {
		return e
	}
	org, ok := q.orgs[p.Certificate.Summary.Config]
	if !ok || p.Certificate.Verify(org) != nil {
		return protocol.ErrAuth
	}
	raw, e := p.MarshalBinary()
	if e != nil {
		return e
	}
	e = q.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		for i := range p.Tx.Body.Outputs {
			id := p.Certificate.Summary.OutputID(uint32(i))
			suffix := protocol.Hash(id).String()
			done := []byte("done/" + suffix)
			key := []byte("pending/" + suffix)
			if _, e := v.Get(done); e == nil {
				continue
			} else if !errors.Is(e, state.ErrNotFound) {
				return nil, e
			}
			if _, e := v.Get(key); e == nil {
				continue
			} else if !errors.Is(e, state.ErrNotFound) {
				return nil, e
			}
			b, e := json.Marshal(record{Payment: raw, Index: uint32(i), Metadata: metadata})
			if e != nil {
				return nil, e
			}
			o.Set(key, b)
		}
		return o.Changes(), nil
	})
	if e == nil {
		select {
		case q.wake <- struct{}{}:
		default:
		}
	}
	return e
}

// Run keeps four slots busy independently. A recipient has at most one active
// delivery; failed recipients back off without holding slots needed by others.
func (q *Outbox) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	type recipientState struct {
		active       bool
		failures     uint
		retry        time.Time
		lastKey      []byte
		eligiblePass bool
	}
	type result struct {
		owner     protocol.PublicKey
		delivered bool
		fatal     error
	}
	recipients := make(map[protocol.PublicKey]*recipientState, len(q.routes))
	results := make(chan result, workers)
	active := 0
	var after []byte
	finish := func(r result) error {
		active--
		st := recipients[r.owner]
		st.active = false
		if r.fatal != nil {
			return r.fatal
		}
		if r.delivered {
			st.failures = 0
			st.retry = time.Time{}
		} else {
			if st.failures < 5 {
				st.failures++
			}
			st.retry = time.Now().Add(100 * time.Millisecond * time.Duration(1<<(st.failures-1)))
		}
		return nil
	}
	for ctx.Err() == nil {
		// Drain completions before scanning another bounded page.
		for draining := true; draining; {
			select {
			case r := <-results:
				if e := finish(r); e != nil {
					return e
				}
			default:
				draining = false
			}
		}
		exhausted := false
		if active < workers {
			if after == nil {
				for _, st := range recipients {
					st.eligiblePass = !st.active && !time.Now().Before(st.retry)
				}
			}
			entries, e := store.Scan(q.db, []byte("pending/"), after, 32)
			if e != nil {
				return e
			}
			if len(entries) == 0 {
				// Wrap a recipient's cursor only after an eligible full scan found
				// no later record. Retain it during backoff, so a rejected first
				// record cannot monopolize that recipient on every retry.
				for _, st := range recipients {
					if st.eligiblePass && !st.active {
						st.lastKey = nil
					}
				}
				after = nil
				exhausted = true
			}
			for _, entry := range entries {
				if active == workers {
					break
				}
				after = entry.Key
				var r record
				if e := json.Unmarshal(entry.Value, &r); e != nil {
					return e
				}
				p, e := protocol.DecodeDirectPayment(r.Payment)
				if e != nil || int(r.Index) >= len(p.Tx.Body.Outputs) {
					return errors.New("corrupt delivery record")
				}
				owner := p.Tx.Body.Outputs[r.Index].Recipient.Owner
				endpoint, ok := q.routes[owner]
				if !ok {
					continue
				}
				st := recipients[owner]
				if st == nil {
					st = &recipientState{}
					recipients[owner] = st
				}
				if st.active || time.Now().Before(st.retry) {
					continue
				}
				if len(st.lastKey) != 0 && bytes.Compare(entry.Key, st.lastKey) <= 0 {
					continue
				}
				st.lastKey = bytes.Clone(entry.Key)
				st.eligiblePass = false // This pass advanced; do not wrap after a fast completion.
				st.active = true
				active++
				wg.Add(1)
				go func(entry state.Entry, r record, owner protocol.PublicKey, endpoint string) {
					defer wg.Done()
					res := result{owner: owner}
					if send(ctx, q.client, endpoint, r) == nil {
						res.fatal = q.db.Update(func(v state.ReadView) ([]state.Change, error) {
							o := state.NewOverlay(v)
							o.Delete(entry.Key)
							o.Set(append([]byte("done/"), entry.Key[len("pending/"):]...), []byte{1})
							return o.Changes(), nil
						})
						res.delivered = res.fatal == nil
					}
					results <- res
				}(entry, r, owner, endpoint)
			}
		}
		// While capacity remains, continue past busy/offline recipients in the next page.
		if active < workers && !exhausted {
			continue
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case r := <-results:
			timer.Stop()
			if e := finish(r); e != nil {
				return e
			}
		case <-q.wake:
			timer.Stop()
			after = nil
		case <-timer.C:
		}
	}
	return nil
}

func send(ctx context.Context, client *http.Client, endpoint string, r record) error {
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(r.Payment))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", transport.MediaType)
	req.Header.Set("X-Next-Output-Index", strconv.FormatUint(uint64(r.Index), 10))
	req.Header.Set(MetadataHeader, r.Metadata)
	resp, e := client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 128))
	if e != nil {
		return e
	}
	p, e := protocol.DecodeDirectPayment(r.Payment)
	if e != nil {
		return e
	}
	id := p.Certificate.Summary.OutputID(r.Index)
	if resp.StatusCode != http.StatusOK || string(b) != protocol.Hash(id).String() {
		return fmt.Errorf("recipient did not acknowledge output: %d", resp.StatusCode)
	}
	return nil
}

// Handler acknowledges only after full wallet verification and store commit.
// after is optional workload bookkeeping, invoked after persistence; it must be
// idempotent because retries can occur after a lost acknowledgement.
func Handler(wallets map[protocol.PublicKey]*wallet.Wallet, after func(protocol.DirectPayment, uint32, string) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != transport.MediaType {
			http.Error(w, "INVALID_ENCODING", 400)
			return
		}
		index, e := strconv.ParseUint(r.Header.Get("X-Next-Output-Index"), 10, 32)
		if e != nil || len(r.Header.Get(MetadataHeader)) > MaxMetadata {
			http.Error(w, "INVALID_INDEX", 400)
			return
		}
		raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, protocol.MaxRequestBytes))
		if e != nil {
			http.Error(w, "INVALID_ENCODING", 400)
			return
		}
		p, e := protocol.DecodeDirectPayment(raw)
		if e != nil || index >= uint64(len(p.Tx.Body.Outputs)) {
			http.Error(w, "INVALID_PAYMENT", 400)
			return
		}
		output := p.Tx.Body.Outputs[index]
		local := wallets[output.Recipient.Owner]
		if local == nil || local.ReceiveDirect(output, p.Certificate, uint32(index)) != nil {
			http.Error(w, "INVALID_CERTIFICATE_OR_RECIPIENT", 400)
			return
		}
		if after != nil {
			if e = after(p, uint32(index), r.Header.Get(MetadataHeader)); e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
		}
		id := p.Certificate.Summary.OutputID(uint32(index))
		io.WriteString(w, protocol.Hash(id).String())
	})
}
