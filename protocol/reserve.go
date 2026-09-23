package protocol

import (
	"crypto/ed25519"
	"encoding/binary"
)

// ReserveIncrease extends an existing grant without invalidating signed requests.
// Its separate funding account is finite and controlled by the grant's Subject.
type ReserveIncrease struct {
	Network, Organization, Grant Hash
	Key                          ResourceKey
	Previous, Amount             uint64
	Subject                      PublicKey
	Signature                    [64]byte
}

func ReserveFundingAccount(network Hash, subject PublicKey) Hash {
	return Digest("RESERVE_FUNDING_ACCOUNT", network[:], subject[:])
}
func IsReserveIncrease(raw []byte) bool {
	return len(raw) >= 2 && binary.BigEndian.Uint16(raw[:2]) == 420
}
func (c ReserveIncrease) body() []byte {
	e := new(Encoder)
	e.U16(420)
	e.Fixed(c.Network[:])
	e.Fixed(c.Organization[:])
	e.Fixed(c.Grant[:])
	e.Fixed(c.Key.Encode())
	e.U64(c.Previous)
	e.U64(c.Amount)
	e.Fixed(c.Subject[:])
	return e.Data()
}
func (c ReserveIncrease) ID() Hash { return Digest("RESERVE_INCREASE", c.body()) }
func (c *ReserveIncrease) Sign(key ed25519.PrivateKey) {
	copy(c.Subject[:], key.Public().(ed25519.PublicKey))
	id := c.ID()
	copy(c.Signature[:], ed25519.Sign(key, id[:]))
}
func (c ReserveIncrease) Verify() error {
	if c.Network == (Hash{}) || c.Organization == (Hash{}) || c.Grant == (Hash{}) || c.Key.Kind != ResourceCAL || c.Key.Account == (Hash{}) || c.Key.Version == 0 || c.Previous == 0 || c.Amount == 0 {
		return ErrRule
	}
	if _, err := Add(c.Previous, c.Amount); err != nil {
		return err
	}
	id := c.ID()
	if !ed25519.Verify(c.Subject[:], id[:], c.Signature[:]) {
		return ErrAuth
	}
	return nil
}
func (c ReserveIncrease) MarshalBinary() ([]byte, error) {
	if err := c.Verify(); err != nil {
		return nil, err
	}
	return append(c.body(), c.Signature[:]...), nil
}
func DecodeReserveIncrease(raw []byte) (c ReserveIncrease, err error) {
	d := NewDecoder(raw)
	if d.U16() != 420 {
		return c, ErrEncoding
	}
	copy(c.Network[:], d.Fixed(32))
	copy(c.Organization[:], d.Fixed(32))
	copy(c.Grant[:], d.Fixed(32))
	c.Key = decodeResource(d)
	c.Previous = d.U64()
	c.Amount = d.U64()
	copy(c.Subject[:], d.Fixed(32))
	copy(c.Signature[:], d.Fixed(64))
	if err = d.Done(); err != nil {
		return c, err
	}
	return c, c.Verify()
}
