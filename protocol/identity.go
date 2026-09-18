package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

const ProtocolVersion uint64 = 2
const WireVersion uint64 = 2

type Hash [32]byte
type TxID Hash
type OutputID Hash
type SpendFactID Hash
type RootID Hash
type SourceID Hash
type FeeID Hash
type Nonce [16]byte
type PublicKey [32]byte
type Signature [64]byte

var ErrAuth = errors.New("invalid authorization")
var ErrRule = errors.New("invalid protocol rule")
var ErrUnsupported = errors.New("unsupported protocol operation")

func (h Hash) String() string               { return hex.EncodeToString(h[:]) }
func (h Hash) MarshalText() ([]byte, error) { return []byte(h.String()), nil }
func (h *Hash) UnmarshalText(b []byte) error {
	if len(b) != 64 {
		return ErrEncoding
	}
	_, e := hex.Decode(h[:], b)
	return e
}

// Digest length-prefixes every component, including the domain.
func Digest(domain string, parts ...[]byte) Hash {
	e := new(Encoder)
	e.Bytes([]byte(domain))
	for _, p := range parts {
		e.Bytes(p)
	}
	return sha256.Sum256(e.Data())
}
func OutputIdentity(network Hash, tx TxID, index uint32) OutputID {
	e := new(Encoder)
	e.U32(index)
	return OutputID(Digest("OUTPUT", network[:], tx[:], e.Data()))
}

type RuleIDs struct{ Fee, Work, Accounting Hash }

func (r RuleIDs) encode(e *Encoder) { e.Fixed(r.Fee[:]); e.Fixed(r.Work[:]); e.Fixed(r.Accounting[:]) }
func decodeRules(d *Decoder) (r RuleIDs) {
	copy(r.Fee[:], d.Fixed(32))
	copy(r.Work[:], d.Fixed(32))
	copy(r.Accounting[:], d.Fixed(32))
	return
}
