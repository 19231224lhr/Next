package protocol

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
)

// Pool actions form a separate, opt-in protocol. Transfer receipts are not
// DirectPayment certificates and have no independent public redemption right.
const (
	PoolDeposit  uint8 = 1
	PoolTransfer uint8 = 2
	PoolExit     uint8 = 3
)
const poolWire uint16 = 430

type PoolAction struct {
	Kind                  uint8
	Network, Config, Pool Hash
	Sequence              uint64
	Previous              Hash
	Input                 OutputID
	Evidence              Hash
	Amount                uint64
	From                  PublicKey
	To                    ReceiveDescriptor
	Signature             Signature
}
type PoolVote struct {
	Member    uint16
	Signature Signature
}
type PoolCertificate struct {
	Action PoolAction
	Votes  []PoolVote
}
type PoolRequest struct {
	Action PoolAction
	Parent *PoolCertificate
}

func PoolIdentity(network, config Hash, input OutputID) Hash {
	return Digest("INTERNAL_POOL_ID_V1", network[:], config[:], input[:])
}
func (a PoolAction) ID() Hash {
	a.Signature = Signature{}
	b, _ := json.Marshal(a)
	return Digest("INTERNAL_POOL_ACTION_V1", b)
}
func (a *PoolAction) Sign(key ed25519.PrivateKey) {
	copy(a.From[:], key.Public().(ed25519.PublicKey))
	h := a.ID()
	copy(a.Signature[:], ed25519.Sign(key, h[:]))
}
func (a PoolAction) Verify(c OrgConfig) error {
	if c.Validate() != nil || a.Network != c.Network || a.Config != c.Hash() || a.Pool == (Hash{}) || a.Amount == 0 || a.To.Verify(a.Network) != nil {
		return ErrAuth
	}
	switch a.Kind {
	case PoolDeposit:
		if a.Sequence != 0 || a.Previous != (Hash{}) || a.Input == (OutputID{}) || a.Evidence == (Hash{}) || a.Pool != PoolIdentity(a.Network, a.Config, a.Input) || a.From != a.To.Owner {
			return ErrRule
		}
	case PoolTransfer, PoolExit:
		if a.Sequence == 0 || a.Sequence == math.MaxUint64 || a.Previous == (Hash{}) || a.Input != (OutputID{}) || a.Evidence != (Hash{}) {
			return ErrRule
		}
	default:
		return ErrRule
	}
	if a.Kind != PoolExit && (a.To.Route.Kind != OrgRoute || a.To.Route.Org != c.Org) {
		return ErrAuth
	}
	h := a.ID()
	if !ed25519.Verify(a.From[:], h[:], a.Signature[:]) {
		return ErrAuth
	}
	return nil
}
func SignPool(a PoolAction, member uint16, key ed25519.PrivateKey) PoolVote {
	id := a.ID()
	h := Digest("INTERNAL_POOL_VOTE_V1", id[:])
	v := PoolVote{Member: member}
	copy(v.Signature[:], ed25519.Sign(key, h[:]))
	return v
}
func (c PoolCertificate) Verify(org OrgConfig) error {
	if err := c.Action.Verify(org); err != nil {
		return err
	}
	if len(c.Votes) < 3 || len(c.Votes) > 4 {
		return ErrAuth
	}
	seen := uint8(0)
	id := c.Action.ID()
	h := Digest("INTERNAL_POOL_VOTE_V1", id[:])
	for _, v := range c.Votes {
		if v.Member >= 4 || seen&(1<<v.Member) != 0 || !ed25519.Verify(org.Members[v.Member][:], h[:], v.Signature[:]) {
			return ErrAuth
		}
		seen |= 1 << v.Member
	}
	return nil
}
func (a PoolAction) OutputID() OutputID {
	return OutputID(Digest("INTERNAL_POOL_EXIT_OUTPUT_V1", a.Network[:], a.Pool[:]))
}
func (a PoolAction) CreationFact() Hash {
	id := a.ID()
	return Digest("INTERNAL_POOL_EXIT_CREATION_V1", id[:])
}
func IsPoolCertificate(raw []byte) bool {
	return len(raw) >= 2 && binary.BigEndian.Uint16(raw[:2]) == poolWire
}
func (c PoolCertificate) MarshalBinary() ([]byte, error) {
	b, e := json.Marshal(c)
	if e != nil {
		return nil, e
	}
	return append([]byte{byte(poolWire >> 8), byte(poolWire & 255)}, b...), nil
}
func DecodePoolCertificate(raw []byte) (c PoolCertificate, err error) {
	if !IsPoolCertificate(raw) || len(raw) > 64<<10 {
		return c, ErrEncoding
	}
	err = decodePoolJSON(raw[2:], &c)
	return
}
func (r PoolRequest) MarshalBinary() ([]byte, error) { return json.Marshal(r) }
func DecodePoolRequest(raw []byte) (r PoolRequest, err error) {
	if len(raw) > 128<<10 {
		return r, ErrEncoding
	}
	err = decodePoolJSON(raw, &r)
	return
}
func decodePoolJSON(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return ErrEncoding
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrEncoding
	}
	return nil
}
