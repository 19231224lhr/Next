package protocol

import (
	"bytes"
	"utxo/crypto/chameleon"
)

const MaxRepairItems = 32

var repairBatchMagic = []byte("RPBATCH4")

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
	return Digest("REPAIR_BATCH_V4", raw)
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
type RepairBatchResult struct {
	Batch      Hash
	Applied    bool
	Effects    []RepairEffect
	FeeOutputs []FeeOutput
}

func (r RepairBatchResult) MarshalBinary() ([]byte, error) {
	if r.Batch == (Hash{}) || len(r.Effects) > MaxRepairItems || len(r.FeeOutputs) > MaxRepairItems*(MaxOutputs+2) || (!r.Applied && (len(r.Effects) != 0 || len(r.FeeOutputs) != 0)) || (r.Applied && len(r.Effects) == 0) {
		return nil, ErrRule
	}
	e := new(Encoder)
	e.U16(412)
	e.Fixed(r.Batch[:])
	e.Optional(r.Applied)
	e.U32(uint32(len(r.Effects)))
	seen := make(map[OutputID]bool, len(r.Effects))
	for _, x := range r.Effects {
		if x.Output == (OutputID{}) || seen[x.Output] || x.ParentFact == (SpendFactID{}) || x.ConsumerFact == (SpendFactID{}) || x.ConsumerTx == (TxID{}) || x.Amount == 0 || x.Debit == (Hash{}) || x.Input >= MaxInputs {
			return nil, ErrRule
		}
		seen[x.Output] = true
		e.Fixed(x.Output[:])
		e.Fixed(x.ParentFact[:])
		e.Fixed(x.ConsumerFact[:])
		e.Fixed(x.ConsumerTx[:])
		e.U32(x.Input)
		e.U64(x.Amount)
		e.Fixed(x.Debit[:])
	}
	e.U32(uint32(len(r.FeeOutputs)))
	for i, f := range r.FeeOutputs {
		if _, err := (ExecutionResult{Applied: true, FeeOutputs: []FeeOutput{f}}).MarshalBinary(); err != nil {
			return nil, err
		}
		if i > 0 {
			p := r.FeeOutputs[i-1]
			n := bytes.Compare(p.Transaction[:], f.Transaction[:])
			if n > 0 || (n == 0 && p.Index >= f.Index) {
				return nil, ErrRule
			}
		}
		e.Fixed(f.Transaction[:])
		e.U32(f.Index)
		encodeOutput(e, f.Output)
	}
	if len(e.Data()) > MaxRequestBytes {
		return nil, ErrEncoding
	}
	return e.Data(), nil
}
func IsRepairBatchResult(b []byte) bool { return len(b) >= 2 && b[0] == 1 && b[1] == 156 }
func DecodeRepairBatchResult(b []byte) (r RepairBatchResult, err error) {
	if len(b) > MaxRequestBytes {
		return r, ErrEncoding
	}
	d := NewDecoder(b)
	if d.U16() != 412 {
		return r, ErrEncoding
	}
	copy(r.Batch[:], d.Fixed(32))
	r.Applied = d.Optional()
	if n := d.Count(MaxRepairItems); n > 0 {
		r.Effects = make([]RepairEffect, n)
	}
	for i := range r.Effects {
		x := &r.Effects[i]
		copy(x.Output[:], d.Fixed(32))
		copy(x.ParentFact[:], d.Fixed(32))
		copy(x.ConsumerFact[:], d.Fixed(32))
		copy(x.ConsumerTx[:], d.Fixed(32))
		x.Input = d.U32()
		x.Amount = d.U64()
		copy(x.Debit[:], d.Fixed(32))
	}
	if n := d.Count(MaxRepairItems * (MaxOutputs + 2)); n > 0 {
		r.FeeOutputs = make([]FeeOutput, n)
	}
	for i := range r.FeeOutputs {
		f := &r.FeeOutputs[i]
		copy(f.Transaction[:], d.Fixed(32))
		f.Index = d.U32()
		f.Output = decodeOutput(d)
	}
	if err = d.Done(); err != nil {
		return r, err
	}
	_, err = r.MarshalBinary()
	return
}

// DecodePublicExecution projects authenticated public effects for followers.
// Callers must first verify the block and its execution-result commitment.
func DecodePublicExecution(network Hash, command, data []byte) (ExecutionResult, []RepairEffect, error) {
	if !IsRepairBatch(command) {
		r, err := DecodeExecution(data)
		return r, nil, err
	}
	c, err := DecodeRepairBatch(command)
	if err != nil {
		return ExecutionResult{}, nil, err
	}
	r, err := DecodeRepairBatchResult(data)
	if err != nil {
		return ExecutionResult{}, nil, err
	}
	if c.Network != network || r.Batch != c.ID() {
		return ExecutionResult{}, nil, ErrAuth
	}
	if r.Applied {
		if len(r.Effects) != len(c.Items) {
			return ExecutionResult{}, nil, ErrAuth
		}
		for i, x := range c.Items {
			e := r.Effects[i]
			if e.Output != x.Output || e.Input != x.Input || e.Debit != ReserveDebitIdentity(network, x.Output) {
				return ExecutionResult{}, nil, ErrAuth
			}
		}
	}
	return ExecutionResult{Applied: r.Applied, FeeOutputs: r.FeeOutputs}, r.Effects, nil
}
