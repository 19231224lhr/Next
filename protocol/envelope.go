package protocol

import (
	"bytes"
)

const (
	MaxSignedTxBytes    = MaxTxBytes + MaxInputs*96 + 32
	MaxCertificateBytes = 96 * 1024
	MaxEvidenceBytes    = 1 << 20
)

func (s SignedTx) VerifyAuth() error {
	if e := s.Body.Validate(); e != nil {
		return e
	}
	if len(s.Auth) == 0 || len(s.Auth) > MaxInputs {
		return ErrAuth
	}
	subject := false
	tx := s.Body.ID()
	for i, a := range s.Auth {
		if i > 0 && bytes.Compare(s.Auth[i-1].Owner[:], a.Owner[:]) >= 0 {
			return ErrAuth
		}
		if e := VerifyOwner(tx, a); e != nil {
			return e
		}
		subject = subject || a.Owner == s.Body.Subject
	}
	if !subject {
		return ErrAuth
	}
	return nil
}
func (s SignedTx) MarshalBinary() ([]byte, error) {
	if e := s.VerifyAuth(); e != nil {
		return nil, e
	}
	b, e := s.Body.MarshalBinary()
	if e != nil {
		return nil, e
	}
	enc := new(Encoder)
	enc.U16(2)
	enc.U64(WireVersion)
	enc.Bytes(b)
	enc.U32(uint32(len(s.Auth)))
	for _, a := range s.Auth {
		enc.Fixed(a.Owner[:])
		enc.Fixed(a.Signature[:])
	}
	return enc.Data(), nil
}
func DecodeSignedTx(b []byte) (s SignedTx, err error) {
	if len(b) > MaxSignedTxBytes {
		return s, ErrEncoding
	}
	d := NewDecoder(b)
	if d.U16() != 2 || d.U64() != WireVersion {
		return s, ErrEncoding
	}
	s.Body, err = DecodeTx(d.Bytes(MaxTxBytes))
	if err != nil {
		return s, err
	}
	s.Auth = make([]OwnerAuth, d.Count(MaxInputs))
	for i := range s.Auth {
		copy(s.Auth[i].Owner[:], d.Fixed(32))
		copy(s.Auth[i].Signature[:], d.Fixed(64))
	}
	if e := d.Done(); e != nil {
		return s, e
	}
	return s, s.VerifyAuth()
}

type CertifiedEffects struct {
	Tx               TxID
	Inputs           []OutputID
	Outputs          []OutputID
	Fee              FeeID
	Depth, Ancestors uint32
}

func FeeIdentity(t TxBody) FeeID {
	e := new(Encoder)
	e.U8(uint8(t.Fee.Source))
	e.Fixed(t.Fee.Account[:])
	e.Fixed(t.Fee.Policy[:])
	e.U64(t.Fee.Version)
	e.U64(t.Fee.Maximum)
	encodeInputs(e, t.Fee.Inputs)
	if t.Fee.Source == OwnerFinalUTXO {
		t.Fee.Refund.encode(e)
	}
	terms := Digest("FEE_TERMS", e.Data())
	id := t.ID()
	return FeeID(Digest("FEE", id[:], terms[:]))
}
func EffectsFor(t TxBody, depth, ancestors uint32) CertifiedEffects {
	f := CertifiedEffects{Tx: t.ID(), Fee: FeeIdentity(t), Depth: depth, Ancestors: ancestors}
	for _, in := range t.Inputs {
		f.Inputs = append(f.Inputs, in.Output)
	}
	for _, in := range t.Fee.Inputs {
		f.Inputs = append(f.Inputs, in.Output)
	}
	for i := range t.Outputs {
		f.Outputs = append(f.Outputs, OutputIdentity(t.Network, t.ID(), uint32(i)))
	}
	return f
}
func (f CertifiedEffects) Encode() []byte {
	e := new(Encoder)
	e.U16(3)
	e.U64(WireVersion)
	e.Fixed(f.Tx[:])
	e.U32(uint32(len(f.Inputs)))
	for _, id := range f.Inputs {
		e.Fixed(id[:])
	}
	e.U32(uint32(len(f.Outputs)))
	for _, id := range f.Outputs {
		e.Fixed(id[:])
	}
	e.Fixed(f.Fee[:])
	e.U32(f.Depth)
	e.U32(f.Ancestors)
	return e.Data()
}
func (f CertifiedEffects) Hash() Hash { return Digest("EFFECTS", f.Encode()) }
func decodeEffects(b []byte) (f CertifiedEffects, err error) {
	d := NewDecoder(b)
	if d.U16() != 3 || d.U64() != WireVersion {
		return f, ErrEncoding
	}
	copy(f.Tx[:], d.Fixed(32))
	f.Inputs = make([]OutputID, d.Count(MaxInputs))
	for i := range f.Inputs {
		copy(f.Inputs[i][:], d.Fixed(32))
	}
	f.Outputs = make([]OutputID, d.Count(MaxOutputs))
	for i := range f.Outputs {
		copy(f.Outputs[i][:], d.Fixed(32))
	}
	copy(f.Fee[:], d.Fixed(32))
	f.Depth = d.U32()
	f.Ancestors = d.U32()
	return f, d.Done()
}

type TXCer struct {
	Tx              SignedTx
	Admission       AdmissionVector
	Effects         CertifiedEffects
	QC              SpendQC
	OutputReference uint32
}

func (c TXCer) Verify(cfg OrgConfig) error {
	t := c.Tx.Body
	if e := c.Tx.VerifyAuth(); e != nil {
		return e
	}
	if t.Kind != FastTransfer || t.Certifier != cfg.Org || t.Network != cfg.Network || t.Epoch != cfg.Epoch || t.Config != cfg.Hash() {
		return ErrAuth
	}
	if e := c.Admission.Validate(); e != nil {
		return e
	}
	if c.OutputReference >= uint32(len(t.Outputs)) || c.Effects.Depth == 0 || c.Effects.Depth > t.Work.Depth || c.Effects.Ancestors == 0 || c.Effects.Ancestors > t.Work.Ancestors {
		return ErrRule
	}
	expected := EffectsFor(t, c.Effects.Depth, c.Effects.Ancestors)
	if !bytes.Equal(expected.Encode(), c.Effects.Encode()) {
		return ErrRule
	}
	if c.QC.Fact != SpendID(t.ID(), c.Admission, c.Effects.Hash(), t.Rules) {
		return ErrAuth
	}
	return VerifyQC(c.QC, cfg)
}
func (c TXCer) MarshalBinary() ([]byte, error) {
	s, e := c.Tx.MarshalBinary()
	if e != nil {
		return nil, e
	}
	if e = c.Admission.Validate(); e != nil {
		return nil, e
	}
	if len(c.QC.Votes) < 3 || len(c.QC.Votes) > 4 {
		return nil, ErrAuth
	}
	enc := new(Encoder)
	enc.U16(4)
	enc.U64(WireVersion)
	enc.Bytes(s)
	enc.Bytes(c.Admission.Encode())
	enc.Bytes(c.Effects.Encode())
	enc.Fixed(c.QC.Fact[:])
	enc.U32(uint32(len(c.QC.Votes)))
	for _, v := range c.QC.Votes {
		enc.U16(v.Member)
		enc.Fixed(v.Signature[:])
	}
	enc.U32(c.OutputReference)
	if len(enc.Data()) > MaxCertificateBytes {
		return nil, ErrEncoding
	}
	return enc.Data(), nil
}
func DecodeCertificate(b []byte) (c TXCer, err error) {
	if len(b) > MaxCertificateBytes {
		return c, ErrEncoding
	}
	d := NewDecoder(b)
	if d.U16() != 4 || d.U64() != WireVersion {
		return c, ErrEncoding
	}
	c.Tx, err = DecodeSignedTx(d.Bytes(MaxSignedTxBytes))
	if err != nil {
		return c, err
	}
	v := NewDecoder(d.Bytes(4096))
	c.Admission = make(AdmissionVector, v.Count(MaxAdmission))
	for i := range c.Admission {
		c.Admission[i] = Allocation{Key: decodeResource(v), Cap: v.U64()}
	}
	if e := v.Done(); e != nil {
		return c, e
	}
	if e := c.Admission.Validate(); e != nil {
		return c, e
	}
	c.Effects, err = decodeEffects(d.Bytes(4096))
	if err != nil {
		return c, err
	}
	copy(c.QC.Fact[:], d.Fixed(32))
	c.QC.Votes = make([]SpendVote, d.Count(4))
	for i := range c.QC.Votes {
		c.QC.Votes[i].Member = d.U16()
		copy(c.QC.Votes[i].Signature[:], d.Fixed(64))
	}
	c.OutputReference = d.U32()
	return c, d.Done()
}
