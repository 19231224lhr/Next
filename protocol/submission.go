package protocol

// Submission distinguishes network delivery attempts from the immutable payment
// identity. Its nonce never enters input consumption, fees or cumulative credit.
type Submission struct {
	Network Hash
	Nonce   Nonce
	Body    []byte
}

func (s Submission) MarshalBinary() ([]byte, error) {
	if s.Network == (Hash{}) || len(s.Body) == 0 || len(s.Body) > MaxCertificateBytes {
		return nil, ErrEncoding
	}
	e := new(Encoder)
	e.U16(82)
	e.U64(WireVersion)
	e.Fixed(s.Network[:])
	e.Fixed(s.Nonce[:])
	e.Bytes(s.Body)
	return e.Data(), nil
}
func DecodeSubmission(raw []byte) (s Submission, err error) {
	if len(raw) > MaxCertificateBytes+128 {
		return s, ErrEncoding
	}
	d := NewDecoder(raw)
	if d.U16() != 82 || d.U64() != WireVersion {
		return s, ErrEncoding
	}
	copy(s.Network[:], d.Fixed(32))
	copy(s.Nonce[:], d.Fixed(16))
	s.Body = append([]byte(nil), d.Bytes(MaxCertificateBytes)...)
	if err = d.Done(); err != nil {
		return
	}
	if s.Network == (Hash{}) || len(s.Body) == 0 {
		err = ErrEncoding
	}
	return
}
