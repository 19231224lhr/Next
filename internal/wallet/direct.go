package wallet

import (
	"errors"
	"utxo/internal/state"
	"utxo/protocol"
)

type DirectCoin struct {
	Output      protocol.Output
	Instance    uint8
	Certificate *protocol.OutputCertificate
	Index       uint32
	Final       protocol.Hash
}

func DirectCoinKey(id protocol.OutputID, instance uint8) []byte {
	return state.Key(state.KeyWalletCertificate, id[:], []byte{instance})
}

func (w *Wallet) SaveDirectRequest(req protocol.DirectRequest) error {
	if req.Tx.Body.Network != w.network || req.Tx.Body.Subject != w.owner {
		return protocol.ErrAuth
	}
	raw, err := req.MarshalBinary()
	if err != nil {
		return err
	}
	return w.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		id := req.Tx.ID()
		intent := req.Tx.Body.Intent
		key := state.Key(state.KeyWalletIntent, intent[:])
		if old, err := v.Get(key); err == nil {
			prior, err := protocol.DecodeDirectRequest(old)
			if err != nil {
				return nil, err
			}
			if prior.Tx.ID() != id {
				return nil, protocol.ErrAuth
			}
			return nil, nil
		} else if !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
		for i, in := range req.Tx.Body.Inputs {
			key := state.Key(state.KeyWalletSpend, in.Output[:], []byte{req.Tx.Claims[i].Instance})
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
		o.Set(key, raw)
		return o.Changes(), nil
	})
}

// ReceiveDirect is the end of the fast-payment measurement: issuer signature
// verified, output binding checked and the complete coin durably saved.
func (w *Wallet) ReceiveDirect(output protocol.Output, c protocol.OutputCertificate, index uint32) error {
	org, ok := w.orgs[c.Summary.Config]
	if !ok || c.Summary.Network != w.network || output.Recipient.Owner != w.owner || c.VerifyOutput(org, index, output) != nil {
		return protocol.ErrAuth
	}
	return w.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		key := DirectCoinKey(c.Summary.OutputID(index), 0)
		late, exists, err := state.Load[DirectCoin](v, DirectCoinKey(c.Summary.OutputID(index), 1))
		if err != nil {
			return nil, err
		}
		if exists {
			if late.Output != output {
				return nil, protocol.ErrAuth
			}
			return nil, nil
		}
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
		if err = state.Put(o, key, DirectCoin{Output: output, Certificate: &c, Index: index}); err != nil {
			return nil, err
		}
		return o.Changes(), nil
	})
}
