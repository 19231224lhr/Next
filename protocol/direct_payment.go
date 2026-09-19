package protocol

// InputCertificate carries only the immediate input certificate, never its history.
type InputCertificate struct {
	Certificate OutputCertificate
	Index       uint32
}
type DirectPayment struct {
	Tx                FastTx
	Certificate       OutputCertificate
	InputCertificates []InputCertificate
}

// MarshalBinary keeps every certificate and authorization in the immutable
// envelope. Only the fixed-width funding references/openings may be revised.
func (p DirectPayment) MarshalBinary() ([]byte, error) {
	raw, err := p.Tx.MarshalBinary()
	if err != nil {
		return nil, err
	}
	fixed, mutable, err := decodeDirectEnvelope(raw)
	if err != nil {
		return nil, err
	}
	if len(p.InputCertificates) > MaxInputs {
		return nil, ErrEncoding
	}
	e := new(Encoder)
	e.U16(405)
	e.Bytes(fixed)
	if p.Certificate.QC.Fact != p.Certificate.Summary.Fact() || len(p.Certificate.QC.Votes) < 3 || len(p.Certificate.QC.Votes) > 4 {
		return nil, ErrAuth
	}
	e.Bytes(p.Certificate.Summary.Admission.Encode())
	e.Fixed(p.Certificate.QC.Fact[:])
	e.U32(uint32(len(p.Certificate.QC.Votes)))
	for _, v := range p.Certificate.QC.Votes {
		e.U16(v.Member)
		e.Fixed(v.Signature[:])
	}
	e.U32(uint32(len(p.InputCertificates)))
	for _, parent := range p.InputCertificates {
		c, err := parent.Certificate.MarshalBinary()
		if err != nil {
			return nil, err
		}
		e.Bytes(c)
		e.U32(parent.Index)
	}
	return encodeDirectEnvelope(e.Data(), mutable)
}

func DecodeDirectPayment(raw []byte) (p DirectPayment, err error) {
	fixed, mutable, err := decodeDirectEnvelope(raw)
	if err != nil {
		return p, err
	}
	d := NewDecoder(fixed)
	if d.U16() != 405 {
		return p, ErrEncoding
	}
	tx, err := encodeDirectEnvelope(d.Bytes(MaxRequestBytes), mutable)
	if err != nil {
		return p, err
	}
	p.Tx, err = DecodeFastTx(tx)
	if err != nil {
		return p, err
	}
	v := NewDecoder(d.Bytes(4096))
	vector := make(AdmissionVector, v.Count(MaxAdmission))
	for i := range vector {
		vector[i] = Allocation{Key: decodeResource(v), Cap: v.U64()}
	}
	if err = v.Done(); err != nil {
		return p, err
	}
	p.Certificate.Summary = SummaryFor(p.Tx, vector)
	copy(p.Certificate.QC.Fact[:], d.Fixed(32))
	p.Certificate.QC.Votes = make([]SpendVote, d.Count(4))
	for i := range p.Certificate.QC.Votes {
		p.Certificate.QC.Votes[i].Member = d.U16()
		copy(p.Certificate.QC.Votes[i].Signature[:], d.Fixed(64))
	}
	p.InputCertificates = make([]InputCertificate, d.Count(MaxInputs))
	for i := range p.InputCertificates {
		p.InputCertificates[i].Certificate, err = DecodeOutputCertificate(d.Bytes(MaxCertificateBytes))
		if err != nil {
			return p, err
		}
		p.InputCertificates[i].Index = d.U32()
	}
	return p, d.Done()
}

func ObligationIdentity(network Hash, output OutputID) Hash {
	return Digest("OBLIGATION", network[:], output[:])
}
func RepairIdentity(network Hash, output OutputID) Hash {
	id := ObligationIdentity(network, output)
	return Digest("REPAIR", network[:], id[:])
}
func ReserveDebitIdentity(network Hash, output OutputID) Hash {
	id := RepairIdentity(network, output)
	return Digest("RESERVE_DEBIT", id[:])
}
