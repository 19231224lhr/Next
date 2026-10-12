package protocol

// DirectContinuationRequest omits parent summaries already retained by their
// signers. The complete parent QC is still required; a local approval is not a QC.
type DirectContinuationRequest struct {
	Tx      FastTx
	Parents []ContinuationParent
}
type ContinuationParent struct {
	QC    SpendQC
	Index uint32
}

func ContinueDirectRequest(r DirectRequest) (DirectContinuationRequest, bool, error) {
	c := DirectContinuationRequest{Tx: r.Tx}
	if len(r.InputCertificates) == 0 {
		return c, false, nil
	}
	for _, p := range r.InputCertificates {
		if p.Certificate.Summary.Config != r.Tx.Body.Config {
			return c, false, nil
		}
		if p.Certificate.Summary.Fact() != p.Certificate.QC.Fact {
			return c, false, ErrAuth
		}
		qc := p.Certificate.QC
		qc.Votes = append([]SpendVote(nil), qc.Votes...)
		c.Parents = append(c.Parents, ContinuationParent{QC: qc, Index: p.Index})
	}
	return c, true, nil
}

// Signers identifies members known to have approved every parent. Other members
// receive the original full request without first attempting a missing reference.
func (r DirectContinuationRequest) Signers() uint8 {
	mask := uint8(15)
	for _, p := range r.Parents {
		var votes uint8
		for _, v := range p.QC.Votes {
			if v.Member < 4 {
				votes |= 1 << v.Member
			}
		}
		mask &= votes
	}
	return mask
}
func (r DirectContinuationRequest) MarshalBinary() ([]byte, error) {
	tx, err := r.Tx.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return r.marshalWithTx(tx)
}

// MarshalForContinuation prepares both encodings once, sharing the authenticated
// transaction encoding across all four recipients and any fallback attempt.
func (r DirectRequest) MarshalForContinuation() (full, compact []byte, signers uint8, err error) {
	full, err = r.MarshalBinary()
	if err != nil {
		return
	}
	c, use, e := ContinueDirectRequest(r)
	if e != nil {
		err = e
		return
	}
	if !use {
		return
	}
	signers = c.Signers()
	if signers == 0 {
		return
	}
	d := NewDecoder(full)
	d.U16()
	compact, err = c.marshalWithTx(d.Bytes(MaxRequestBytes))
	return
}

func (r DirectContinuationRequest) marshalWithTx(tx []byte) ([]byte, error) {
	if len(r.Parents) > MaxInputs {
		return nil, ErrEncoding
	}
	e := new(Encoder)
	e.U16(308)
	e.Bytes(tx)
	e.U32(uint32(len(r.Parents)))
	for _, p := range r.Parents {
		if len(p.QC.Votes) < 3 || len(p.QC.Votes) > 4 {
			return nil, ErrEncoding
		}
		e.Fixed(p.QC.Fact[:])
		e.U32(p.Index)
		e.U32(uint32(len(p.QC.Votes)))
		for _, v := range p.QC.Votes {
			e.U16(v.Member)
			e.Fixed(v.Signature[:])
		}
	}
	if len(e.Data()) > MaxRequestBytes {
		return nil, ErrEncoding
	}
	return e.Data(), nil
}
func IsDirectContinuationRequest(raw []byte) bool {
	return len(raw) >= 2 && NewDecoder(raw).U16() == 308
}
func DecodeDirectContinuationRequest(raw []byte) (r DirectContinuationRequest, err error) {
	if len(raw) > MaxRequestBytes {
		return r, ErrEncoding
	}
	d := NewDecoder(raw)
	if d.U16() != 308 {
		return r, ErrEncoding
	}
	r.Tx, err = DecodeFastTx(d.Bytes(MaxRequestBytes))
	if err != nil {
		return r, err
	}
	r.Parents = make([]ContinuationParent, d.Count(MaxInputs))
	for i := range r.Parents {
		p := &r.Parents[i]
		copy(p.QC.Fact[:], d.Fixed(32))
		p.Index = d.U32()
		p.QC.Votes = make([]SpendVote, d.Count(4))
		if len(p.QC.Votes) < 3 {
			return r, ErrEncoding
		}
		for j := range p.QC.Votes {
			v := &p.QC.Votes[j]
			v.Member = d.U16()
			copy(v.Signature[:], d.Fixed(64))
		}
	}
	return r, d.Done()
}
