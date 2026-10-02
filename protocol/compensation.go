package protocol

import "bytes"

const compensationResultTag uint16 = 415

// CompensationDecision identifies a committed responsibility and its exact
// historical input. All submitters produce the same bytes; no adapter data.
type CompensationDecision struct {
	Network            Hash
	Output             OutputID
	Height             int64
	Transaction, Input uint32
}

func IsCompensationDecision(b []byte) bool {
	return len(b) >= 8 && bytes.Equal(b[:8], []byte("CMPDEC05"))
}
func (c CompensationDecision) MarshalBinary() ([]byte, error) {
	if c.Network == (Hash{}) || c.Output == (OutputID{}) || c.Height <= 0 || c.Input >= MaxInputs {
		return nil, ErrRule
	}
	e := new(Encoder)
	e.Fixed([]byte("CMPDEC05"))
	e.Fixed(c.Network[:])
	e.Fixed(c.Output[:])
	e.U64(uint64(c.Height))
	e.U32(c.Transaction)
	e.U32(c.Input)
	return e.Data(), nil
}
func (c CompensationDecision) ID() Hash {
	raw, err := c.MarshalBinary()
	if err != nil {
		return Hash{}
	}
	return Digest("COMPENSATION_DECISION_V5", raw)
}
func DecodeCompensationDecision(b []byte) (c CompensationDecision, err error) {
	if len(b) != 88 || !IsCompensationDecision(b) {
		return c, ErrEncoding
	}
	d := NewDecoder(b)
	d.Fixed(8)
	copy(c.Network[:], d.Fixed(32))
	copy(c.Output[:], d.Fixed(32))
	c.Height = int64(d.U64())
	c.Transaction = d.U32()
	c.Input = d.U32()
	if err = d.Done(); err != nil {
		return
	}
	_, err = c.MarshalBinary()
	return
}

// RepairResult reports representation progress, never economic effects.
type RepairResult struct {
	Command Hash
	Applied bool
}

func (r RepairResult) MarshalBinary() ([]byte, error) {
	if r.Command == (Hash{}) {
		return nil, ErrRule
	}
	e := new(Encoder)
	e.U16(416)
	e.Fixed(r.Command[:])
	e.Optional(r.Applied)
	return e.Data(), nil
}
func DecodeRepairResult(b []byte) (r RepairResult, err error) {
	d := NewDecoder(b)
	if d.U16() != 416 {
		return r, ErrEncoding
	}
	copy(r.Command[:], d.Fixed(32))
	r.Applied = d.Optional()
	if err = d.Done(); err != nil {
		return
	}
	_, err = r.MarshalBinary()
	return
}

// DecodePublicExecution projects authenticated economic effects. A successful
// representation command is intentionally economically inert to followers.
func DecodePublicExecution(network Hash, command, data []byte) (ExecutionResult, []RepairEffect, error) {
	if IsCompensationDecision(command) {
		c, err := DecodeCompensationDecision(command)
		if err != nil {
			return ExecutionResult{}, nil, err
		}
		r, err := DecodeCompensationResult(data)
		if err != nil {
			return ExecutionResult{}, nil, err
		}
		if c.Network != network || r.Decision != c.ID() {
			return ExecutionResult{}, nil, ErrAuth
		}
		if r.Applied {
			if len(r.Effects) != 1 {
				return ExecutionResult{}, nil, ErrAuth
			}
			e := r.Effects[0]
			if e.Output != c.Output || e.Input != c.Input || e.Debit != ReserveDebitIdentity(network, c.Output) {
				return ExecutionResult{}, nil, ErrAuth
			}
		}
		return ExecutionResult{Applied: r.Applied, FeeOutputs: r.FeeOutputs}, r.Effects, nil
	}
	if IsRepairBatch(command) || IsRepairInput(command) {
		var id, net Hash
		if IsRepairBatch(command) {
			c, err := DecodeRepairBatch(command)
			if err != nil {
				return ExecutionResult{}, nil, err
			}
			id, net = c.ID(), c.Network
		} else {
			c, err := DecodeRepairInput(command)
			if err != nil {
				return ExecutionResult{}, nil, err
			}
			id, net = c.ID(), c.Network
		}
		r, err := DecodeRepairResult(data)
		if err != nil {
			return ExecutionResult{}, nil, err
		}
		if net != network || r.Command != id {
			return ExecutionResult{}, nil, ErrAuth
		}
		return ExecutionResult{}, nil, nil
	}
	r, err := DecodeExecution(data)
	return r, nil, err
}

type CompensationResult struct {
	Decision   Hash
	Applied    bool
	Effects    []RepairEffect
	FeeOutputs []FeeOutput
}

func (r CompensationResult) MarshalBinary() ([]byte, error) {
	if r.Decision == (Hash{}) || len(r.Effects) > MaxRepairItems || len(r.FeeOutputs) > MaxRepairItems*(MaxOutputs+2) || (!r.Applied && (len(r.Effects) != 0 || len(r.FeeOutputs) != 0)) || (r.Applied && len(r.Effects) == 0) {
		return nil, ErrRule
	}
	e := new(Encoder)
	e.U16(compensationResultTag)
	e.Fixed(r.Decision[:])
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
func IsCompensationResult(b []byte) bool {
	return len(b) >= 2 && uint16(b[0])<<8|uint16(b[1]) == compensationResultTag
}
func DecodeCompensationResult(b []byte) (r CompensationResult, err error) {
	if len(b) > MaxRequestBytes {
		return r, ErrEncoding
	}
	d := NewDecoder(b)
	if d.U16() != compensationResultTag {
		return r, ErrEncoding
	}
	copy(r.Decision[:], d.Fixed(32))
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
