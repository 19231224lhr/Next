package state

import (
	"encoding/json"
	"errors"
	"utxo/protocol"
)

const (
	KeyCreation uint8 = 10
	KeySpend    uint8 = 11
	KeyApproval uint8 = 20
	KeyInstall  uint8 = 21
	KeyIntent   uint8 = 22
	KeySlice    uint8 = 30
	KeyGrant    uint8 = 31
	KeyOutbox   uint8 = 40
	KeyGenesis  uint8 = 50
	KeyPayment  uint8 = 80
	KeyAccount  uint8 = 81
	KeyReward   uint8 = 82
	KeyBurned   uint8 = 83
	KeyUsage    uint8 = 84
	KeyCredit   uint8 = 85
	KeyObserved uint8 = 86
)

func Key(kind uint8, parts ...[]byte) []byte {
	e := new(protocol.Encoder)
	e.U16(2)
	e.U8(kind)
	for _, p := range parts {
		e.Bytes(p)
	}
	return e.Data()
}
func SliceKey(k protocol.ResourceKey, worker uint32) []byte {
	e := new(protocol.Encoder)
	e.U32(worker)
	return Key(KeySlice, k.Encode(), e.Data())
}
func Load[T any](v ReadView, key []byte) (value T, found bool, err error) {
	b, e := v.Get(key)
	if errors.Is(e, ErrNotFound) {
		return value, false, nil
	}
	if e != nil {
		return value, false, e
	}
	e = json.Unmarshal(b, &value)
	return value, e == nil, e
}
func Put[T any](o *Overlay, key []byte, value T) error {
	b, e := json.Marshal(value)
	if e != nil {
		return e
	}
	o.Set(key, b)
	return nil
}

type OriginOutput struct {
	ID     protocol.OutputID
	Output protocol.Output
	Fact   protocol.Hash
}
type Grant struct {
	ID           protocol.Hash
	Organization protocol.Hash
	Key          protocol.ResourceKey
	Amount       uint64
	Subject      protocol.PublicKey
}
type Genesis struct {
	Network protocol.Hash
	Outputs []OriginOutput
	Grants  []Grant
}

func (g Genesis) Hash() protocol.Hash {
	b, _ := json.Marshal(g)
	return protocol.Digest("GENESIS_CONFIG", b)
}

type Creation struct {
	Output protocol.Output
	Fact   protocol.Hash
	Source protocol.Hash
	Final  bool
}
type Spend struct {
	Candidate protocol.SpendFactID
	Consumed  protocol.SpendFactID
}
type Slice struct{ Available, Reserved, Generation uint64 }
type Debit struct {
	Key          protocol.ResourceKey
	Cap, Applied uint64
	Worker       uint32
}
type Approval struct {
	Fact      protocol.SpendFactID
	Tx        protocol.SignedTx
	Admission protocol.AdmissionVector
	Effects   protocol.CertifiedEffects
	Debits    []Debit
	Parents   [][]byte
}
type Outbox struct {
	Fact        protocol.SpendFactID
	Certificate []byte
	Origin      protocol.Hash
}

type Transition struct {
	Changes []Change
	Facts   []protocol.FinalFact
}

const KeyDirect uint8 = 87

const KeyCollected uint8 = 88

const (
	KeyWalletIntent      uint8 = 89
	KeyWalletSpend       uint8 = 90
	KeyWalletCertificate uint8 = 91
)

const KeyCustody uint8 = 92

type Custody struct {
	Fact, Certificate, Effects protocol.Hash
	Proof                      []byte
}

const (
	KeyWaiting          uint8 = 93
	KeyDependencyQueue  uint8 = 94
	KeyDependencyItem   uint8 = 95
	KeyDependencyQueued uint8 = 96
)
