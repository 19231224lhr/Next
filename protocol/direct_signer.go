package protocol

import (
	"bytes"
	"crypto/ed25519"
	"slices"
)

// DirectSigner owns a validated key. It only signs unsigned, single-owner
// requests; it never accepts a caller assertion that an existing signature is
// valid. Use DirectRequest.MarshalBinary for externally signed requests.
type DirectSigner struct {
	key   ed25519.PrivateKey
	owner PublicKey
}

func NewDirectSigner(key ed25519.PrivateKey) (*DirectSigner, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, ErrAuth
	}
	owned := ed25519.NewKeyFromSeed(key[:ed25519.SeedSize])
	if !bytes.Equal(owned, key) {
		return nil, ErrAuth
	}
	s := &DirectSigner{key: owned}
	copy(s.owner[:], owned[ed25519.SeedSize:])
	return s, nil
}

func (s *DirectSigner) Owner() PublicKey { return s.owner }

// Sign encodes an owned transaction snapshot with one core encoding and TxID.
// No authorization cache survives this call. As with other encoding APIs, the
// caller must not concurrently mutate the input while this call reads it.
func (s *DirectSigner) Sign(r DirectRequest) (FastTx, TxID, []byte, error) {
	fail := func(err error) (FastTx, TxID, []byte, error) { return FastTx{}, TxID{}, nil, err }
	if s == nil || len(s.key) != ed25519.PrivateKeySize || len(r.Tx.Auth) != 0 || r.Tx.Body.Subject != s.owner {
		return fail(ErrAuth)
	}
	t := r.Tx
	t.Body.Inputs = slices.Clone(t.Body.Inputs)
	t.Body.Outputs = slices.Clone(t.Body.Outputs)
	t.Body.Admission = slices.Clone(t.Body.Admission)
	t.Body.Fee.Inputs = slices.Clone(t.Body.Fee.Inputs)
	t.Claims = slices.Clone(t.Claims)
	t.Commitments = slices.Clone(t.Commitments)
	t.Funding = slices.Clone(t.Funding)
	t.Auth = []OwnerAuth{{Owner: s.owner}}
	if err := t.verifyAuthFields(); err != nil {
		return fail(err)
	}
	core := t.core()
	id := t.idWithCore(core)
	t.Auth[0] = SignOwner(id, s.key)
	encoded, err := t.marshalWithCore(core)
	if err != nil {
		return fail(err)
	}
	raw, err := r.marshalWithTx(encoded)
	if err != nil {
		return fail(err)
	}
	return t, id, raw, nil
}
