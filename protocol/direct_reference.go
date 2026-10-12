package protocol

// CertificateReference binds the entire certificate, including its quorum votes.
// A fact ID alone must not turn a forged certificate into a cache hit.
func CertificateReference(c OutputCertificate) (Hash, error) {
	raw, err := c.MarshalBinary()
	if err != nil {
		return Hash{}, err
	}
	return Digest("DIRECT_CERTIFICATE_REFERENCE", raw), nil
}

type InputCertificateReference struct {
	Certificate Hash
	Index       uint32
}
type DirectReferenceRequest struct {
	Tx      FastTx
	Parents []InputCertificateReference
}

func ReferenceDirectRequest(r DirectRequest) (DirectReferenceRequest, bool, error) {
	compact := DirectReferenceRequest{Tx: r.Tx}
	if len(r.InputCertificates) == 0 {
		return compact, false, nil
	}
	for _, parent := range r.InputCertificates {
		if parent.Certificate.Summary.Config != r.Tx.Body.Config {
			return compact, false, nil
		}
		ref, err := CertificateReference(parent.Certificate)
		if err != nil {
			return compact, false, err
		}
		compact.Parents = append(compact.Parents, InputCertificateReference{Certificate: ref, Index: parent.Index})
	}
	return compact, true, nil
}

func (r DirectReferenceRequest) MarshalBinary() ([]byte, error) {
	tx, err := r.Tx.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if len(r.Parents) > MaxInputs {
		return nil, ErrEncoding
	}
	e := new(Encoder)
	e.U16(307)
	e.Bytes(tx)
	e.U32(uint32(len(r.Parents)))
	for _, p := range r.Parents {
		e.Fixed(p.Certificate[:])
		e.U32(p.Index)
	}
	if len(e.Data()) > MaxRequestBytes {
		return nil, ErrEncoding
	}
	return e.Data(), nil
}

func IsDirectReferenceRequest(raw []byte) bool { return len(raw) >= 2 && NewDecoder(raw).U16() == 307 }

func DecodeDirectReferenceRequest(raw []byte) (r DirectReferenceRequest, err error) {
	if len(raw) > MaxRequestBytes {
		return r, ErrEncoding
	}
	d := NewDecoder(raw)
	if d.U16() != 307 {
		return r, ErrEncoding
	}
	r.Tx, err = DecodeFastTx(d.Bytes(MaxRequestBytes))
	if err != nil {
		return r, err
	}
	r.Parents = make([]InputCertificateReference, d.Count(MaxInputs))
	for i := range r.Parents {
		copy(r.Parents[i].Certificate[:], d.Fixed(32))
		r.Parents[i].Index = d.U32()
	}
	return r, d.Done()
}
