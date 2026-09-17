package protocol

import (
	"bytes"
)

type FactKind uint16

const (
	FactGrant          FactKind = 1
	FactOutputCreated  FactKind = 2
	FactOutputConsumed FactKind = 3
	FactCredit         FactKind = 4
	FactWork           FactKind = 5
	FactCustody        FactKind = 6
	FactFeeClosed      FactKind = 7
	FactRootFunded     FactKind = 8
)
const MaxFactBytes = 1 << 20

type FinalFact struct {
	Kind     FactKind
	Key      Hash
	Revision uint64
	Network  Hash
	Rules    RuleIDs
	Payload  []byte
}

func (f FinalFact) MarshalBinary() ([]byte, error) {
	if f.Kind < FactGrant || f.Kind > FactRootFunded || f.Key == (Hash{}) || f.Network == (Hash{}) || f.Revision == 0 || len(f.Payload) > MaxFactBytes {
		return nil, ErrRule
	}
	e := new(Encoder)
	e.U16(50)
	e.U64(WireVersion)
	e.U16(uint16(f.Kind))
	e.Fixed(f.Key[:])
	e.U64(f.Revision)
	e.Fixed(f.Network[:])
	f.Rules.encode(e)
	e.Bytes(f.Payload)
	return e.Data(), nil
}
func DecodeFact(b []byte) (f FinalFact, err error) {
	if len(b) > MaxFactBytes+256 {
		return f, ErrEncoding
	}
	d := NewDecoder(b)
	if d.U16() != 50 || d.U64() != WireVersion {
		return f, ErrEncoding
	}
	f.Kind = FactKind(d.U16())
	copy(f.Key[:], d.Fixed(32))
	f.Revision = d.U64()
	copy(f.Network[:], d.Fixed(32))
	f.Rules = decodeRules(d)
	f.Payload = bytes.Clone(d.Bytes(MaxFactBytes))
	if e := d.Done(); e != nil {
		return f, e
	}
	_, err = f.MarshalBinary()
	return
}
func (f FinalFact) ID() Hash { b, _ := f.MarshalBinary(); return Digest("FACT", b) }
func (f FinalFact) SortKey() []byte {
	e := new(Encoder)
	e.U16(uint16(f.Kind))
	e.Fixed(f.Key[:])
	e.U64(f.Revision)
	return e.Data()
}
