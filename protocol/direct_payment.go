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

// DirectSubmission is the public transaction, not a newly issued output TXCer.
// Authorization reuses the organization's existing votes for this spend. Its
// signed summary is reconstructed from Tx and Admission, never sent twice.
type DirectSubmission struct {
	Tx                FastTx
	Admission         AdmissionVector
	Authorization     SpendQC
	InputCertificates []InputCertificate
}

func (p DirectPayment) Submission() DirectSubmission {
	return DirectSubmission{Tx: p.Tx, Admission: p.Certificate.Summary.Admission,
		Authorization: p.Certificate.QC, InputCertificates: p.InputCertificates}
}

func (p DirectSubmission) Summary() OutputSummary { return SummaryFor(p.Tx, p.Admission) }

// Only fixed-width funding references/openings may be revised in either format.
func (p DirectPayment) MarshalBinary() ([]byte, error) {
	if p.Certificate.Summary.Fact() != p.Submission().Summary().Fact() {
		return nil, ErrAuth
	}
	return p.Submission().marshal(405)
}

func (p DirectSubmission) MarshalBinary() ([]byte, error) { return p.marshal(406) }

func (p DirectSubmission) marshal(kind uint16) ([]byte, error) {
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
	e.U16(kind)
	e.Bytes(fixed)
	if p.Authorization.Fact != p.Summary().Fact() || len(p.Authorization.Votes) < 3 || len(p.Authorization.Votes) > 4 {
		return nil, ErrAuth
	}
	e.Bytes(p.Admission.Encode())
	e.Fixed(p.Authorization.Fact[:])
	e.U32(uint32(len(p.Authorization.Votes)))
	for _, v := range p.Authorization.Votes {
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

func DecodeDirectPayment(raw []byte) (DirectPayment, error) {
	p, err := decodeDirectSubmission(raw, 405)
	if err != nil {
		return DirectPayment{}, err
	}
	return DirectPayment{Tx: p.Tx, Certificate: OutputCertificate{Summary: p.Summary(), QC: p.Authorization}, InputCertificates: p.InputCertificates}, nil
}

func DecodeDirectSubmission(raw []byte) (DirectSubmission, error) {
	return decodeDirectSubmission(raw, 406)
}

func decodeDirectSubmission(raw []byte, kind uint16) (p DirectSubmission, err error) {
	fixed, mutable, err := decodeDirectEnvelope(raw)
	if err != nil {
		return p, err
	}
	d := NewDecoder(fixed)
	if d.U16() != kind {
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
	p.Admission = vector
	copy(p.Authorization.Fact[:], d.Fixed(32))
	p.Authorization.Votes = make([]SpendVote, d.Count(4))
	for i := range p.Authorization.Votes {
		p.Authorization.Votes[i].Member = d.U16()
		copy(p.Authorization.Votes[i].Signature[:], d.Fixed(64))
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
