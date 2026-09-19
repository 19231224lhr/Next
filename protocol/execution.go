package protocol

// ExecutionResult records only execution choices not derivable from the signed
// transaction: which inputs were absent, and which outputs are late instances.
// No balances, member budgets, notification targets or credit proofs are sent.
type ExecutionResult struct {
	Applied                    bool
	MissingInputs, LateOutputs []uint32
}

func (r ExecutionResult) MarshalBinary() ([]byte, error) {
	e := new(Encoder)
	e.U16(410)
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
	return e.Data(), nil
}
func DecodeExecution(b []byte) (r ExecutionResult, err error) {
	d := NewDecoder(b)
	if d.U16() != 410 {
		return r, ErrEncoding
	}
	r.Applied = d.Optional()
	for _, v := range []*[]uint32{&r.MissingInputs, &r.LateOutputs} {
		*v = make([]uint32, d.Count(MaxInputs+MaxOutputs))
		for i := range *v {
			(*v)[i] = d.U32()
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
