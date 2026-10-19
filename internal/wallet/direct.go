package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

type DirectCoin struct {
	Output      protocol.Output
	Instance    uint8
	Certificate *protocol.OutputCertificate
	Index       uint32
	Final       protocol.Hash
	// Recovered is a terminal observation, not a spendable coin. The source
	// executed after compensation and its value repaid the reserve payer.
	Recovered bool `json:",omitempty"`
}

func DirectCoinKey(id protocol.OutputID, instance uint8) []byte {
	return state.Key(state.KeyWalletCertificate, id[:], []byte{instance})
}

func (w *Wallet) SaveDirectRequest(req protocol.DirectRequest) error {
	_, err := w.SaveDirectRequestBytes(req)
	return err
}

// SaveDirectRequestBytes returns the exact signed request only after its request
// journal and input reservations have been committed by the configured Store.
func (w *Wallet) SaveDirectRequestBytes(req protocol.DirectRequest) ([]byte, error) {
	if err := w.validateDirectWallet(req.Tx); err != nil {
		return nil, err
	}
	raw, err := req.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return w.saveDirectBytes(req.Tx, req.Tx.ID(), raw)
}

func (w *Wallet) validateDirectWallet(tx protocol.FastTx) error {
	if w.direct != nil {
		if err := w.direct.ValidateC3(tx.Body.Fee.C3, tx.Body.Config); err != nil {
			return err
		}
	} else if tx.Body.Fee.C3.Enabled() {
		return protocol.ErrAuth
	}
	if w.bound == (protocol.Hash{}) || tx.Body.Network != w.network || tx.Body.Subject != w.owner {
		return protocol.ErrAuth
	}
	return nil
}

// Only authenticated/locally signed bytes enter this private persistence path.
func (w *Wallet) saveDirectBytes(tx protocol.FastTx, id protocol.TxID, raw []byte) ([]byte, error) {
	err := w.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if err := rules.ValidateWalletDescriptors(o, tx); err != nil {
			return nil, err
		}
		key := state.Key(state.KeyWalletRequest, id[:])
		if old, err := v.Get(key); err == nil {
			if !bytes.Equal(old, raw) {
				return nil, protocol.ErrAuth
			}
			return nil, nil
		} else if !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
		for i, in := range tx.Body.Inputs {
			coin, found, err := w.loadDirectCoinStatus(o, DirectCoinKey(in.Output, tx.Claims[i].Instance))
			if err != nil {
				return nil, err
			}
			if found && coin.Recovered {
				return nil, protocol.ErrAuth
			}
			key := state.Key(state.KeyWalletSpend, in.Output[:], []byte{tx.Claims[i].Instance})
			prior, found, err := state.Load[protocol.TxID](o, key)
			if err != nil {
				return nil, err
			}
			if found && prior != id {
				return nil, protocol.ErrAuth
			}
			if err = state.Put(o, key, id); err != nil {
				return nil, err
			}
		}
		for _, in := range tx.Body.Fee.Inputs {
			k := state.Key(state.KeyWalletSpend, in.Output[:], []byte{0})
			prior, found, err := state.Load[protocol.TxID](o, k)
			if err != nil {
				return nil, err
			}
			if found && prior != id {
				return nil, protocol.ErrAuth
			}
			if err = state.Put(o, k, id); err != nil {
				return nil, err
			}
		}
		o.Set(key, raw)
		return o.Changes(), nil
	})
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// ReceiveDirect is the end of the fast-payment measurement: issuer signature
// verified, output binding checked and the local coin transaction committed.
// Durability depends on the configured Store; memory/NoSync is experimental.
func (w *Wallet) ReceiveDirect(output protocol.Output, c protocol.OutputCertificate, index uint32) error {
	if w.direct != nil {
		if err := rules.ValidateC3Summary(c.Summary, *w.direct); err != nil {
			return err
		}
	} else if c.Summary.C3.Enabled() {
		return protocol.ErrAuth
	}
	org, ok := w.orgs[c.Summary.Config]
	if w.bound == (protocol.Hash{}) || !ok || c.Summary.Network != w.network || output.Recipient.Owner != w.owner || c.VerifyOutput(org, index, output) != nil {
		return protocol.ErrAuth
	}
	if output.Recipient.Route.Kind != protocol.OrgRoute || output.Recipient.Route.Org != w.bound {
		return protocol.ErrAuth
	}
	key := DirectCoinKey(c.Summary.OutputID(index), 0)
	coin := DirectCoin{Output: output, Certificate: &c, Index: index}
	var saved []byte
	err := w.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		old, found, err := state.Load[DirectCoin](v, key)
		if err != nil {
			return nil, err
		}
		if found {
			if old.Output != output {
				return nil, protocol.ErrAuth
			}
			return nil, nil
		}
		saved, err = json.Marshal(coin)
		if err != nil {
			return nil, err
		}
		o.Set(key, saved)
		return o.Changes(), nil
	})
	if err == nil && saved != nil {
		w.coinStatus.rememberCoin(key, saved, coin)
	}
	return err
}
