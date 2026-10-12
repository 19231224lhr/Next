package wallet

import (
	"bytes"
	"errors"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type Wallet struct {
	db      store.Store
	network protocol.Hash
	owner   protocol.PublicKey
	orgs    map[protocol.Hash]protocol.OrgConfig
	bound   protocol.Hash
}

func New(db store.Store, network protocol.Hash, owner protocol.PublicKey, orgs []protocol.OrgConfig) (*Wallet, error) {
	if db == nil || network == (protocol.Hash{}) || owner == (protocol.PublicKey{}) {
		return nil, protocol.ErrRule
	}
	if err := protocol.ValidateOrganizations(network, orgs); err != nil {
		return nil, err
	}
	w := &Wallet{db: db, network: network, owner: owner, orgs: make(map[protocol.Hash]protocol.OrgConfig)}
	for _, o := range orgs {
		w.orgs[o.Hash()] = o
	}
	if err := db.View(func(v state.ReadView) error {
		binding, _, err := state.Load[protocol.Hash](v, state.Key(state.KeyWalletBinding, owner[:]))
		w.bound = binding
		return err
	}); err != nil {
		return nil, err
	}
	return w, nil
}

// NewWithGenesis loads the fixed wallet routes used by the direct protocol.
func NewWithGenesis(db store.Store, network protocol.Hash, owner protocol.PublicKey, orgs []protocol.OrgConfig, genesis state.Genesis) (*Wallet, error) {
	w, err := New(db, network, owner, orgs)
	if err != nil {
		return nil, err
	}
	if genesis.Network != network {
		return nil, protocol.ErrAuth
	}
	var bound protocol.Hash
	err = db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if err := rules.InitializeWalletBindings(o, genesis); err != nil {
			return nil, err
		}
		binding, found, err := state.Load[protocol.Hash](o, state.Key(state.KeyWalletBinding, owner[:]))
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, protocol.ErrAuth
		}
		known := false
		for _, org := range orgs {
			if org.Org == binding {
				known = true
			}
		}
		if !known {
			return nil, protocol.ErrAuth
		}
		bound = binding
		return o.Changes(), nil
	})
	if err != nil {
		return nil, err
	}
	w.bound = bound
	return w, nil
}

// SaveRequest fixes the exact signed request and chosen inputs before submission.
// A timeout keeps the request pending and its inputs reserved.
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
		key := state.Key(state.KeyWalletRequest, id[:])
		if previous, e := v.Get(key); e == nil {
			if !bytes.Equal(previous, raw) {
				return nil, errors.New("wallet transaction already fixes a different request")
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
