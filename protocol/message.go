package protocol

import (
	"bytes"
	"crypto/ed25519"
)

type PaymentRequest struct {
	Tx      SignedTx
	Parents []TXCer
}
type Approval struct {
	Fact      SpendFactID
	Vote      SpendVote
	Admission AdmissionVector
	Effects   CertifiedEffects
}

func (r PaymentRequest) MarshalBinary() ([]byte, error) {
	tx, e := r.Tx.MarshalBinary()
	if e != nil {
		return nil, e
	}
	if len(r.Parents) > MaxInputs {
		return nil, ErrEncoding
	}
	enc := new(Encoder)
	enc.U16(80)
	enc.U64(WireVersion)
	enc.Bytes(tx)
	enc.U32(uint32(len(r.Parents)))
	for _, c := range r.Parents {
		raw, e := c.MarshalBinary()
		if e != nil {
			return nil, e
		}
		enc.Bytes(raw)
	}
	if len(enc.Data()) > MaxRequestBytes {
		return nil, ErrEncoding
	}
	return enc.Data(), nil
}

const MaxRequestBytes = MaxEvidenceBytes + MaxSignedTxBytes + 128

func DecodeRequest(raw []byte) (r PaymentRequest, err error) {
	if len(raw) > MaxRequestBytes {
		return r, ErrEncoding
	}
	d := NewDecoder(raw)
	if d.U16() != 80 || d.U64() != WireVersion {
		return r, ErrEncoding
	}
	r.Tx, err = DecodeSignedTx(d.Bytes(MaxSignedTxBytes))
	if err != nil {
		return
	}
	r.Parents = make([]TXCer, d.Count(MaxInputs))
	for i := range r.Parents {
		r.Parents[i], err = DecodeCertificate(d.Bytes(MaxCertificateBytes))
		if err != nil {
			return
		}
	}
	err = d.Done()
	return
}
func (a Approval) MarshalBinary() ([]byte, error) {
	if e := a.Admission.Validate(); e != nil {
		return nil, e
	}
	enc := new(Encoder)
	enc.U16(81)
	enc.U64(WireVersion)
	enc.Fixed(a.Fact[:])
	enc.U16(a.Vote.Member)
	enc.Fixed(a.Vote.Signature[:])
	enc.Bytes(a.Admission.Encode())
	enc.Bytes(a.Effects.Encode())
	return enc.Data(), nil
}
func DecodeApproval(raw []byte) (a Approval, err error) {
	if len(raw) > 16384 {
		return a, ErrEncoding
	}
	d := NewDecoder(raw)
	if d.U16() != 81 || d.U64() != WireVersion {
		return a, ErrEncoding
	}
	copy(a.Fact[:], d.Fixed(32))
	a.Vote.Member = d.U16()
	copy(a.Vote.Signature[:], d.Fixed(64))
	v := NewDecoder(d.Bytes(4096))
	a.Admission = make(AdmissionVector, v.Count(MaxAdmission))
	for i := range a.Admission {
		a.Admission[i] = Allocation{Key: decodeResource(v), Cap: v.U64()}
	}
	if err = v.Done(); err != nil {
		return
	}
	if err = a.Admission.Validate(); err != nil {
		return
	}
	a.Effects, err = decodeEffects(d.Bytes(4096))
	if err != nil {
		return
	}
	err = d.Done()
	return
}
func (a Approval) Verify(tx SignedTx, cfg OrgConfig) error {
	t := tx.Body
	if cfg.Validate() != nil || t.Network != cfg.Network || t.Certifier != cfg.Org || t.Config != cfg.Hash() || t.Epoch != cfg.Epoch || a.Vote.Member >= 4 {
		return ErrAuth
	}
	if e := tx.VerifyAuth(); e != nil {
		return e
	}
	if e := a.Admission.Validate(); e != nil {
		return e
	}
	if a.Effects.Depth == 0 || a.Effects.Depth > t.Work.Depth || a.Effects.Ancestors == 0 || a.Effects.Ancestors > t.Work.Ancestors {
		return ErrRule
	}
	expected := EffectsFor(t, a.Effects.Depth, a.Effects.Ancestors)
	if !bytes.Equal(expected.Encode(), a.Effects.Encode()) {
		return ErrRule
	}
	if a.Fact != SpendID(t.ID(), a.Admission, a.Effects.Hash(), t.Rules) {
		return ErrAuth
	}
	digest := Digest("SPEND_VOTE", a.Fact[:])
	if !ed25519.Verify(cfg.Members[a.Vote.Member][:], digest[:], a.Vote.Signature[:]) {
		return ErrAuth
	}
	return nil
}
