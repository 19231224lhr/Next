package protocol

import "bytes"

// Fee outputs are final public change/refund, never outputs certified by TXCer.
const FeeChangeIndex uint32 = MaxOutputs
const FeeRefundIndex uint32 = MaxOutputs + 1

// Transaction, index, asset, amount, and the fixed receive descriptor.
const FeeOutputBytes uint64 = 32 + 4 + 1 + 8 + 8 + 32 + 32 + 1 + 32 + 64

type FeeOutput struct {
	Transaction TxID
	Index       uint32
	Output      Output
}

// ExecutionResult records only execution choices not derivable from the signed
// transaction: missing inputs, late instances, and final fee change/refunds.
// It contains public ledger effects, not private notifications or credit proofs.
type ExecutionResult struct {
	Applied                    bool
	MissingInputs, LateOutputs []uint32
	FeeOutputs                 []FeeOutput
}

func (r ExecutionResult) MarshalBinary() ([]byte, error) {
	e := new(Encoder)
	if len(r.FeeOutputs) == 0 {
		e.U16(410)
	} else {
		e.U16(411)
	}
	e.Optional(r.Applied)
	for _, v := range [][]uint32{r.MissingInputs, r.LateOutputs} {
		if len(v) > MaxInputs+MaxOutputs {
			return nil, ErrEncoding
		}
		e.U32(uint32(len(v)))
		for i, n := range v {
			if i > 0 && v[i-1] >= n {
				return nil, ErrEncoding
			}
			e.U32(n)
		}
	}
	if len(r.FeeOutputs) > 0 {
		if !r.Applied || len(r.FeeOutputs) > MaxOutputs+2 {
			return nil, ErrEncoding
		}
		e.U32(uint32(len(r.FeeOutputs)))
		for i, f := range r.FeeOutputs {
			if f.Transaction == (TxID{}) || (f.Index != FeeChangeIndex && f.Index != FeeRefundIndex) || f.Output.Asset != AssetFUEL || f.Output.Amount == 0 {
				return nil, ErrEncoding
			}
			if i > 0 {
				prev := r.FeeOutputs[i-1]
				c := bytes.Compare(prev.Transaction[:], f.Transaction[:])
				if c > 0 || (c == 0 && prev.Index >= f.Index) {
					return nil, ErrEncoding
				}
			}
			e.Fixed(f.Transaction[:])
			e.U32(f.Index)
			encodeOutput(e, f.Output)
		}
	}
	return e.Data(), nil
}
func DecodeExecution(b []byte) (r ExecutionResult, err error) {
	d := NewDecoder(b)
	tag := d.U16()
	if tag != 410 && tag != 411 {
		return r, ErrEncoding
	}
	r.Applied = d.Optional()
	for _, v := range []*[]uint32{&r.MissingInputs, &r.LateOutputs} {
		*v = make([]uint32, d.Count(MaxInputs+MaxOutputs))
		for i := range *v {
			(*v)[i] = d.U32()
		}
	}
	if tag == 411 {
		r.FeeOutputs = make([]FeeOutput, d.Count(MaxOutputs+2))
		if len(r.FeeOutputs) == 0 {
			return r, ErrEncoding
		}
		for i := range r.FeeOutputs {
			f := &r.FeeOutputs[i]
			copy(f.Transaction[:], d.Fixed(32))
			f.Index = d.U32()
			f.Output = decodeOutput(d)
		}
	}
	if err = d.Done(); err != nil {
		return r, err
	}
	_, err = r.MarshalBinary()
	return
}
func CreationIdentity(network Hash, tx TxID, index uint32, instance uint8) Hash {
	e := new(Encoder)
	e.U32(index)
	e.U8(instance)
	return Digest("CREATION_V4", network[:], tx[:], e.Data())
}
