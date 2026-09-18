package gateway

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"time"
	"utxo/finality"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type PublicClient interface {
	Submit(context.Context, []byte) error
	Receipt(context.Context, protocol.FactKind, protocol.Hash) (finality.FactProof, error)
	Certificate(context.Context, protocol.SpendFactID) (protocol.TXCer, error)
}
type Relay struct {
	DB            store.Store
	Public        PublicClient
	Trust         finality.Trust
	Organizations map[protocol.Hash]protocol.OrgConfig
	Members       map[protocol.Hash][4]MemberClient
	Apply         func(finality.FactProof) error
	// Reconciles a certificate obtained from the committee when only an original
	// local vote remained. A local member supplies Install for its own issuer.
	Install func(protocol.TXCer) error
}

func (r *Relay) Run(ctx context.Context) error {
	if r.DB == nil || r.Public == nil || r.Trust.Validate() != nil {
		return protocol.ErrRule
	}
	var cursor []byte
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			entries, e := store.Scan(r.DB, state.Key(state.KeyOutbox), cursor, 16)
			if e != nil {
				return e
			}
			if len(entries) == 0 {
				cursor = nil
				continue
			}
			for _, entry := range entries {
				if ctx.Err() != nil {
					return nil
				}
				cursor = entry.Key
				var pending state.Outbox
				if e = json.Unmarshal(entry.Value, &pending); e != nil {
					return e
				}
				// Retry errors are retained in the durable queue; transport failures do not
				// cancel financial commitments.
				operation, cancel := context.WithTimeout(ctx, 5*time.Second)
				_ = r.deliver(operation, entry.Key, pending)
				cancel()
			}
		}
	}
}
func (r *Relay) deliver(ctx context.Context, key []byte, pending state.Outbox) (err error) {
	var c protocol.TXCer
	var e error
	if len(pending.Certificate) == 0 {
		c, e = r.Public.Certificate(ctx, pending.Fact)
	} else {
		c, e = protocol.DecodeCertificate(pending.Certificate)
	}
	if e != nil {
		return e
	}
	org, known := r.Organizations[c.Tx.Body.Config]
	if !known || c.QC.Fact != pending.Fact {
		return protocol.ErrAuth
	}
	if e = c.Verify(org); e != nil {
		return e
	}
	if r.Install != nil {
		if e = r.Install(c); e != nil {
			return e
		}
	}

	if clients, ok := r.Members[c.Tx.Body.Certifier]; ok {
		installCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{}, 4)
		for _, client := range clients {
			go func(client MemberClient) {
				if client != nil {
					_ = client.Install(installCtx, c)
				}
				done <- struct{}{}
			}(client)
		}
		// Receipt/custody processing is independent of acknowledgements. Bound the
		// four attempts to this pass, cancelling and joining them on return.
		defer func() {
			cancel()
			for i := 0; i < 4; i++ {
				<-done
			}
		}()
	}

	// Query proofs before retrying, including after restart. Register this defer
	// after INSTALL cleanup so public submission never waits for INSTALL ACKs.
	publicComplete := false
	defer func() {
		if err == nil || ctx.Err() != nil {
			return
		}
		if publicComplete {
			if e := r.markPublicComplete(key); e != nil {
				err = e
			}
			return
		}
		if e := r.submitDue(ctx, key, c); e != nil {
			err = e
		}
	}()
	var custody bool
	for _, allocation := range c.Admission {
		kind := protocol.FactCredit
		if allocation.Key.Kind == protocol.ResourceExecution {
			kind = protocol.FactWork
		}
		if allocation.Key.Kind == protocol.ResourceBytes {
			kind = protocol.FactCustody
		}
		keyReceipt := (protocol.CreditReceipt{Spend: c.QC.Fact, Resource: allocation.Key}).Key()
		// Reserve time for submission if the proof endpoint stalls.
		queryCtx, cancelQuery := context.WithTimeout(ctx, time.Second)
		proof, e := r.Public.Receipt(queryCtx, kind, keyReceipt)
		cancelQuery()
		if e != nil {
			return e
		}
		verified, e := finality.Verify(r.Trust, proof)
		if e != nil {
			return e
		}
		fact := verified.Fact()
		receipt, e := protocol.DecodeCredit(fact.Payload)
		if kind == protocol.FactCustody {
			custodyReceipt, err := protocol.DecodeCustody(fact.Payload)
			e = err
			receipt = custodyReceipt.Credit
			if e == nil && custodyReceipt.Effects != c.Effects.Hash() {
				return protocol.ErrAuth
			}
		}
		if e != nil {
			return e
		}
		if fact.Kind != kind || fact.Key != keyReceipt || fact.Rules != c.Tx.Body.Rules || receipt.Spend != c.QC.Fact || receipt.Resource != allocation.Key || receipt.Original != allocation.Cap {
			return protocol.ErrAuth
		}
		if kind == protocol.FactCustody {
			custody = true
		}
		// A terminal execution receipt proves the business work completed. Other
		// receipts may still be unavailable and must continue to be fetched.
		if kind == protocol.FactWork && receipt.Remaining == 0 && receipt.Discharged == receipt.Original && receipt.Paid == 0 && receipt.Returned == 0 {
			publicComplete = true
		}
		if r.Apply != nil {
			if e = r.Apply(proof); e != nil {
				return e
			}
		}
	}
	if !custody {
		return errors.New("public custody not established")
	}
	return r.DB.Update(func(v state.ReadView) ([]state.Change, error) {
		// The queue's fact identity cannot be repurposed by an alternative QC subset.
		old, found, e := state.Load[state.Outbox](v, key)
		if e != nil {
			return nil, e
		}
		if !found {
			return nil, nil
		}
		if old.Fact != pending.Fact {
			return nil, protocol.ErrAuth
		}
		return []state.Change{{Key: key, Delete: true}}, nil
	})
}

const retryInterval = time.Second
const attemptLifetime = 5 * time.Second

func (r *Relay) submitDue(ctx context.Context, key []byte, c protocol.TXCer) error {
	var raw []byte
	err := r.DB.Update(func(v state.ReadView) ([]state.Change, error) {
		p, found, err := state.Load[state.Outbox](v, key)
		if err != nil || !found {
			return nil, err
		}
		if p.Fact != c.QC.Fact {
			return nil, protocol.ErrAuth
		}
		now := time.Now()
		if p.PublicComplete || now.UnixNano() < p.NextSubmitUnixNS {
			return nil, nil
		}
		if len(p.Attempt) == 0 || now.Sub(time.Unix(0, p.AttemptStartedUnixNS)) >= attemptLifetime {
			body, err := c.MarshalBinary()
			if err != nil {
				return nil, err
			}
			attempt := protocol.Submission{Network: c.Tx.Body.Network, Body: body}
			// Identical holders share the initial envelope. Only prolonged lack of
			// progress creates a new nonce to escape a stale Comet transaction cache.
			if len(p.Attempt) != 0 {
				if _, err = rand.Read(attempt.Nonce[:]); err != nil {
					return nil, err
				}
			}
			p.Attempt, err = attempt.MarshalBinary()
			if err != nil {
				return nil, err
			}
			p.AttemptStartedUnixNS = now.UnixNano()
		}
		p.NextSubmitUnixNS = now.Add(retryInterval).UnixNano()
		raw = p.Attempt
		o := state.NewOverlay(v)
		if err := state.Put(o, key, p); err != nil {
			return nil, err
		}
		return o.Changes(), nil
	})
	if err != nil || len(raw) == 0 {
		return err
	}
	return r.Public.Submit(ctx, raw)
}

func (r *Relay) markPublicComplete(key []byte) error {
	return r.DB.Update(func(v state.ReadView) ([]state.Change, error) {
		p, found, err := state.Load[state.Outbox](v, key)
		if err != nil || !found || p.PublicComplete {
			return nil, err
		}
		p.PublicComplete = true
		o := state.NewOverlay(v)
		if err = state.Put(o, key, p); err != nil {
			return nil, err
		}
		return o.Changes(), nil
	})
}
