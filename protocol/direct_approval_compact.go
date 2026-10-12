package protocol

const MaxCompactApprovalBytes = 2 + 4 + 49*MaxAdmission + 2 + 64

// MarshalCompact omits fields deterministically reconstructed from the original
// request. The vote still signs the complete original output summary.
func (a DirectApproval) MarshalCompact() ([]byte, error) {
	if err := a.Summary.Admission.Validate(); err != nil {
		return nil, err
	}
	if a.Vote.Member >= 4 {
		return nil, ErrEncoding
	}
	e := new(Encoder)
	e.U16(309)
	e.Fixed(a.Summary.Admission.Encode())
	e.U16(a.Vote.Member)
	e.Fixed(a.Vote.Signature[:])
	return e.Data(), nil
}

// DecodeCompactDirectApproval only decodes and reconstructs. Collectors must
// authenticate the returned vote and bind its member index to the contacted peer.
func DecodeCompactDirectApproval(raw []byte, tx FastTx) (a DirectApproval, err error) {
	if len(raw) > MaxCompactApprovalBytes {
		return a, ErrEncoding
	}
	d := NewDecoder(raw)
	if d.U16() != 309 {
		return a, ErrEncoding
	}
	v := make(AdmissionVector, d.Count(MaxAdmission))
	for i := range v {
		v[i] = Allocation{Key: decodeResource(d), Cap: d.U64()}
	}
	a.Vote.Member = d.U16()
	copy(a.Vote.Signature[:], d.Fixed(64))
	if err = d.Done(); err != nil {
		return a, err
	}
	if a.Vote.Member >= 4 {
		return a, ErrEncoding
	}
	if err = v.Validate(); err != nil {
		return a, err
	}
	a.Summary = SummaryFor(tx, v)
	return a, nil
}
