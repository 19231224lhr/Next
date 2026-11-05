package protocol

import (
	"bytes"
	"utxo/crypto/chameleon"
)

const MaxRepairItems = 32

var repairBatchMagic = []byte("RPBATCH5")

type RepairItem struct {
	Output             OutputID
	Transaction, Input uint32
	Opening            chameleon.Opening
}
type RepairBatch struct {
	Network        Hash
	Height         int64
	Base           uint64
	Previous, Next Hash
	Items          []RepairItem
	Parts          []chameleon.Opening
}

func IsRepairBatch(b []byte) bool { return len(b) >= 8 && bytes.Equal(b[:8], repairBatchMagic) }
func (r RepairBatch) ID() Hash {
	raw, err := r.MarshalBinary()
	if err != nil {
		return Hash{}
	}
	return Digest("REPAIR_BATCH_V5", raw)
}
func (r RepairBatch) MarshalBinary() ([]byte, error) {
	if r.Network == (Hash{}) || r.Height <= 0 || r.Base == ^uint64(0) || len(r.Items) == 0 || len(r.Items) > MaxRepairItems || len(r.Parts) == 0 || len(r.Parts) > 16384 {
		return nil, ErrRule
	}
	e := new(Encoder)
	e.Fixed(repairBatchMagic)
	e.Fixed(r.Network[:])
	e.U64(uint64(r.Height))
	e.U64(r.Base)
	e.Fixed(r.Previous[:])
	e.Fixed(r.Next[:])
	e.U32(uint32(len(r.Items)))
	seen := make(map[OutputID]bool, len(r.Items))
	for i, x := range r.Items {
		if x.Output == (OutputID{}) || seen[x.Output] || x.Input >= MaxInputs {
			return nil, ErrRule
		}
		if i > 0 {
			p := r.Items[i-1]
			if p.Transaction > x.Transaction || (p.Transaction == x.Transaction && p.Input >= x.Input) {
				return nil, ErrRule
			}
		}
		seen[x.Output] = true
		e.Fixed(x.Output[:])
		e.U32(x.Transaction)
		e.U32(x.Input)
		e.Fixed(x.Opening[:])
	}
	e.U32(uint32(len(r.Parts)))
	for _, p := range r.Parts {
		e.Fixed(p[:])
	}
	if len(e.Data()) > MaxRequestBytes {
		return nil, ErrEncoding
	}
	return e.Data(), nil
}
func DecodeRepairBatch(b []byte) (r RepairBatch, err error) {
	if len(b) > MaxRequestBytes || !IsRepairBatch(b) {
		return r, ErrEncoding
	}
	d := NewDecoder(b)
	d.Fixed(8)
	copy(r.Network[:], d.Fixed(32))
	r.Height = int64(d.U64())
	r.Base = d.U64()
	copy(r.Previous[:], d.Fixed(32))
	copy(r.Next[:], d.Fixed(32))
	r.Items = make([]RepairItem, d.Count(MaxRepairItems))
	for i := range r.Items {
		x := &r.Items[i]
		copy(x.Output[:], d.Fixed(32))
		x.Transaction = d.U32()
		x.Input = d.U32()
		copy(x.Opening[:], d.Fixed(chameleon.Size))
	}
	r.Parts = make([]chameleon.Opening, d.Count(16384))
	for i := range r.Parts {
		copy(r.Parts[i][:], d.Fixed(chameleon.Size))
	}
	if err = d.Done(); err != nil {
		return r, err
	}
	_, err = r.MarshalBinary()
	return
}

// RepairEffect is derived from committed responsibility, not caller assertions.
type RepairEffect struct {
	Output                   OutputID
	ParentFact, ConsumerFact SpendFactID
	ConsumerTx               TxID
	Input                    uint32
	Amount                   uint64
	Debit                    Hash
}
