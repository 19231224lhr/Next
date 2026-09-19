package protocol

type DirectRequest struct {
	Tx                FastTx
	InputCertificates []InputCertificate
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
	if len(r.InputCertificates) > MaxInputs {
		return nil, ErrEncoding
	}
	e := new(Encoder)
	e.U16(306)
	e.Bytes(tx)
	e.U32(uint32(len(r.InputCertificates)))
	for _, p := range r.InputCertificates {
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
	r.InputCertificates = make([]InputCertificate, d.Count(MaxInputs))
	for i := range r.InputCertificates {
		r.InputCertificates[i].Certificate, err = DecodeOutputCertificate(d.Bytes(MaxCertificateBytes))
		if err != nil {
			return r, err
		}
		r.InputCertificates[i].Index = d.U32()
	}
	return r, d.Done()
}
