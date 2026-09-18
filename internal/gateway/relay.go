package gateway

import (
	"context"
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
func (r *Relay) deliver(ctx context.Context, key []byte, pending state.Outbox) error {
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
	raw, e := c.MarshalBinary()
	if e != nil {
		return e
	}
	// Public submission does not wait for any member INSTALL acknowledgement.
	_ = r.Public.Submit(ctx, raw)

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
		proof, e := r.Public.Receipt(ctx, kind, keyReceipt)
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
