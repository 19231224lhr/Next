package chameleon

import (
	"crypto/rand"
	tss "github.com/cloudflare/circl/tss/rsa"
)

// GenerateDealer creates one fixed laboratory committee. Only individual shares
// are returned; the complete private exponent is never persisted or distributed.
func GenerateDealer() (*Public, [4][]byte, error) {
	var encoded [4][]byte
	key, err := tss.GenerateKey(rand.Reader, Size*8)
	if err != nil {
		return nil, encoded, err
	}
	shares, err := tss.Deal(rand.Reader, 4, 3, key, true)
	if err != nil {
		return nil, encoded, err
	}
	public, err := NewPublic(&key.PublicKey)
	if err != nil {
		return nil, encoded, err
	}
	for i := range shares {
		encoded[i], err = shares[i].MarshalBinary()
		if err != nil {
			return nil, encoded, err
		}
	}
	return public, encoded, nil
}
func (p *Public) Modulus() []byte { return p.key.N.Bytes() }
func (s Signer) Index() uint      { return s.share.Index }
func DecodeSigner(p *Public, b []byte) (Signer, error) {
	if len(b) == 0 || len(b) > 4096 {
		return Signer{}, ErrInvalid
	}
	var share tss.KeyShare
	if err := share.UnmarshalBinary(b); err != nil {
		return Signer{}, err
	}
	return NewSigner(p, share)
}
func (c Contribution) MarshalBinary() ([]byte, error) { return c.share.MarshalBinary() }
func DecodeContribution(b []byte) (Contribution, error) {
	if len(b) == 0 || len(b) > 4096 {
		return Contribution{}, ErrInvalid
	}
	var share tss.SignShare
	if err := share.UnmarshalBinary(b); err != nil {
		return Contribution{}, err
	}
	return Contribution{share: share}, nil
}
