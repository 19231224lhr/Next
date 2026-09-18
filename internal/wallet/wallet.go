package wallet

import (
	"errors"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type Wallet struct {
	db      store.Store
	network protocol.Hash
	owner   protocol.PublicKey
	orgs    map[protocol.Hash]protocol.OrgConfig
}

func New(db store.Store, network protocol.Hash, owner protocol.PublicKey, orgs []protocol.OrgConfig) (*Wallet, error) {
	if db == nil || network == (protocol.Hash{}) || owner == (protocol.PublicKey{}) {
		return nil, protocol.ErrRule
	}
	w := &Wallet{db: db, network: network, owner: owner, orgs: make(map[protocol.Hash]protocol.OrgConfig)}
	for _, o := range orgs {
		if o.Validate() != nil || o.Network != network {
			return nil, protocol.ErrRule
		}
		w.orgs[o.Hash()] = o
	}
	return w, nil
}

// SaveRequest fixes the exact signed intent and chosen inputs before submission.
// A timeout leaves this intent pending; it never makes its inputs selectable again.
func (w *Wallet) SaveRequest(request protocol.PaymentRequest) error {
	if request.Tx.Body.Network != w.network || request.Tx.Body.Subject != w.owner {
		return protocol.ErrAuth
	}
	raw, e := request.MarshalBinary()
	if e != nil {
		return e
	}
	return w.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		id := request.Tx.Body.ID()
		intent := request.Tx.Body.Intent
		key := state.Key(state.KeyWalletIntent, intent[:])
		if previous, e := v.Get(key); e == nil {
			old, e := protocol.DecodeRequest(previous)
			if e != nil {
				return nil, e
			}
			if old.Tx.Body.ID() != id {
				return nil, errors.New("wallet intent already fixes a different body")
			}
			return nil, nil
		} else if !errors.Is(e, state.ErrNotFound) {
			return nil, e
		}
		for _, group := range [][]protocol.Input{request.Tx.Body.Inputs, request.Tx.Body.Fee.Inputs} {
			for _, in := range group {
				k := state.Key(state.KeyWalletSpend, in.Output[:])
				prior, found, e := state.Load[protocol.TxID](o, k)
				if e != nil {
					return nil, e
				}
				if found && prior != id {
					return nil, errors.New("wallet input already reserved")
				}
				if e = state.Put(o, k, id); e != nil {
					return nil, e
				}
			}
		}
		o.Set(key, raw)
		return o.Changes(), nil
	})
}
func (w *Wallet) Receive(c protocol.TXCer, index uint32) error {
	org, known := w.orgs[c.Tx.Body.Config]
	if !known {
		return protocol.ErrAuth
	}
	if e := c.Verify(org); e != nil {
		return e
	}
	if index >= uint32(len(c.Tx.Body.Outputs)) || c.Tx.Body.Outputs[index].Recipient.Owner != w.owner {
		return protocol.ErrAuth
	}
	c.OutputReference = index
	raw, e := c.MarshalBinary()
	if e != nil {
		return e
	}
	return w.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		id := c.Effects.Outputs[index]
		key := state.Key(state.KeyWalletCertificate, id[:])
		if old, e := v.Get(key); e == nil {
			previous, e := protocol.DecodeCertificate(old)
			if e != nil {
				return nil, e
			}
			if previous.QC.Fact != c.QC.Fact {
				return nil, protocol.ErrAuth
			}
			return nil, nil
		} else if !errors.Is(e, state.ErrNotFound) {
			return nil, e
		}
		o.Set(key, raw)
		if e = state.Put(o, state.Key(state.KeyOutbox, c.QC.Fact[:]), state.Outbox{Fact: c.QC.Fact, Certificate: raw, Origin: c.Tx.Body.Certifier}); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	})
}
