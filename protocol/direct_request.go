package protocol

type DirectRequest struct {
	Tx      FastTx
	Parents []DirectParent
}
type DirectApproval struct {
	Summary OutputSummary
	Vote    SpendVote
}

func (r DirectRequest) MarshalBinary() ([]byte, error) {
	tx, err := r.Tx.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if len(r.Parents) > MaxInputs {
		return nil, ErrEncoding
	}
	e := new(Encoder)
	e.U16(306)
	e.Bytes(tx)
	e.U32(uint32(len(r.Parents)))
	for _, p := range r.Parents {
		b, err := p.Certificate.MarshalBinary()
		if err != nil {
			return nil, err
		}
		e.Bytes(b)
		e.U32(p.Index)
	}
	if len(e.Data()) > MaxRequestBytes {
		return nil, ErrEncoding
	}
	return e.Data(), nil
}
func DecodeDirectRequest(raw []byte) (r DirectRequest, err error) {
	if len(raw) > MaxRequestBytes {
		return r, ErrEncoding
	}
	d := NewDecoder(raw)
	if d.U16() != 306 {
		return r, ErrEncoding
	}
	r.Tx, err = DecodeFastTx(d.Bytes(MaxRequestBytes))
	if err != nil {
		return r, err
	}
	r.Parents = make([]DirectParent, d.Count(MaxInputs))
	for i := range r.Parents {
		r.Parents[i].Certificate, err = DecodeOutputCertificate(d.Bytes(MaxCertificateBytes))
		if err != nil {
			return r, err
		}
		r.Parents[i].Index = d.U32()
	}
	return r, d.Done()
}
