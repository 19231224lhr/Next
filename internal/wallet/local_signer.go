package wallet

import (
	"crypto/ed25519"
	"utxo/protocol"
)

type LocalDirectSigner struct {
	wallet *Wallet
	signer *protocol.DirectSigner
}

func (w *Wallet) NewLocalDirectSigner(key ed25519.PrivateKey) (*LocalDirectSigner, error) {
	s, err := protocol.NewDirectSigner(key)
	if err != nil {
		return nil, err
	}
	if s.Owner() != w.owner {
		return nil, protocol.ErrAuth
	}
	return &LocalDirectSigner{wallet: w, signer: s}, nil
}

// SignAndSave only returns sendable bytes after the atomic request journal and
// CAL/FUEL reservations commit. Existing signed requests use SaveDirectRequestBytes.
func (s *LocalDirectSigner) SignAndSave(req protocol.DirectRequest) (protocol.FastTx, protocol.TxID, []byte, error) {
	fail := func(err error) (protocol.FastTx, protocol.TxID, []byte, error) {
		return protocol.FastTx{}, protocol.TxID{}, nil, err
	}
	if s == nil || s.wallet == nil || s.signer == nil {
		return fail(protocol.ErrAuth)
	}
	tx, id, raw, err := s.signer.Sign(req)
	if err != nil {
		return fail(err)
	}
	if err = s.wallet.validateDirectWallet(tx); err != nil {
		return fail(err)
	}
	raw, err = s.wallet.saveDirectBytes(tx, id, raw)
	if err != nil {
		return fail(err)
	}
	return tx, id, raw, nil
}
