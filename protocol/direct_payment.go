package protocol

// DirectParent carries only the immediate input certificate, never its history.
type DirectParent struct {
	Certificate OutputCertificate
	Index       uint32
}
type DirectPayment struct {
	Tx          FastTx
	Certificate OutputCertificate
	Parents     []DirectParent
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
	if len(p.Parents) > MaxInputs {
		return nil, ErrEncoding
	}
	e := new(Encoder)
	e.U16(305)
	e.Bytes(fixed)
	c, err := p.Certificate.MarshalBinary()
	if err != nil {
		return nil, err
	}
	e.Bytes(c)
	e.U32(uint32(len(p.Parents)))
	for _, parent := range p.Parents {
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
	if d.U16() != 305 {
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
	p.Certificate, err = DecodeOutputCertificate(d.Bytes(MaxCertificateBytes))
	if err != nil {
		return p, err
	}
	p.Parents = make([]DirectParent, d.Count(MaxInputs))
	for i := range p.Parents {
		p.Parents[i].Certificate, err = DecodeOutputCertificate(d.Bytes(MaxCertificateBytes))
		if err != nil {
			return p, err
		}
		p.Parents[i].Index = d.U32()
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
