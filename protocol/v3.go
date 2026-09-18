package protocol

import (
	"bytes"
	"encoding/binary"
	"utxo/crypto/chameleon"
)

const DirectVersion uint64 = 3

var directMagic = []byte{'U', 'T', 'X', 'O', '3', 'C', 'H', 0}

// InputClaim is immutable. Instance 1 is created only by a finalized late parent
// after compensation; an old certificate never authorizes that instance.
type InputClaim struct {
	Output   Output
	Instance uint8
}
type Funding struct {
	Kind    uint8
	Ref     Hash
	Opening chameleon.Opening
}

const (
	OriginalFunding uint8 = 0
	ReserveFunding  uint8 = 1
)

// FastTx keeps ownership/output semantics fixed while its funding openings can
// be repaired. The existing body fields, descriptors and signatures are reused.
type FastTx struct {
	Body        TxBody
	Claims      []InputClaim
	Commitments []chameleon.Commitment
	Funding     []Funding
	Auth        []OwnerAuth
}

func encodeOutput(e *Encoder, o Output) { e.U8(uint8(o.Asset)); e.U64(o.Amount); o.Recipient.encode(e) }
func decodeOutput(d *Decoder) Output {
	return Output{Asset: Asset(d.U8()), Amount: d.U64(), Recipient: decodeDescriptor(d)}
}
func OutputDigest(o Output) Hash {
	e := new(Encoder)
	encodeOutput(e, o)
	return Digest("OUTPUT_BODY_V3", e.Data())
}

func (t FastTx) core() []byte {
	e := new(Encoder)
	e.Bytes(t.Body.encode())
	e.U32(uint32(len(t.Claims)))
	for _, c := range t.Claims {
		encodeOutput(e, c.Output)
		e.U8(c.Instance)
	}
	return e.Data()
}
func (t FastTx) ID() TxID {
	e := new(Encoder)
	e.Bytes(t.core())
	for _, c := range t.Commitments {
		e.Fixed(c[:])
	}
	return TxID(Digest("TX_V3", e.Data()))
}
func (t FastTx) FundingContext(index int, key [32]byte) []byte {
	core := Digest("TX_CORE_V3", t.core())
	e := new(Encoder)
	e.Fixed(t.Body.Network[:])
	e.Fixed(core[:])
	e.U32(uint32(index))
	e.Fixed(key[:])
	h := Digest("INPUT_CH_V3", e.Data())
	return h[:]
}
func (f Funding) ReferenceBytes() []byte {
	e := new(Encoder)
	e.U8(f.Kind)
	e.Fixed(f.Ref[:])
	return e.Data()
}

func NewFastTx(body TxBody, claims []InputClaim, key *chameleon.Public) (FastTx, error) {
	t := FastTx{Body: body, Claims: claims}
	if err := body.validateVersion(3, 3); err != nil {
		return t, err
	}
	if len(claims) != len(body.Inputs) || key == nil {
		return t, ErrRule
	}
	t.Commitments = make([]chameleon.Commitment, len(claims))
	t.Funding = make([]Funding, len(claims))
	for i, in := range body.Inputs {
		f := Funding{Ref: Hash(in.Output)}
		c, r, err := key.Commit(t.FundingContext(i, key.KeyID()), f.ReferenceBytes())
		if err != nil {
			return FastTx{}, err
		}
		f.Opening = r
		t.Commitments[i] = c
		t.Funding[i] = f
	}
	return t, nil
}

func (t FastTx) VerifyAuth() error {
	if err := t.Body.validateVersion(3, 3); err != nil {
		return err
	}
	if len(t.Claims) != len(t.Body.Inputs) || len(t.Funding) != len(t.Claims) || len(t.Commitments) != len(t.Claims) || len(t.Auth) == 0 || len(t.Auth) > MaxInputs {
		return ErrRule
	}
	owners := make(map[PublicKey]bool, len(t.Auth))
	id := t.ID()
	for i, a := range t.Auth {
		if i > 0 && bytes.Compare(t.Auth[i-1].Owner[:], a.Owner[:]) >= 0 {
			return ErrAuth
		}
		if err := VerifyOwner(id, a); err != nil {
			return err
		}
		owners[a.Owner] = true
	}
	if !owners[t.Body.Subject] {
		return ErrAuth
	}
	for i, c := range t.Claims {
		if c.Output.Asset != AssetCAL || c.Output.Amount == 0 || c.Instance > 1 || c.Output.Recipient.Verify(t.Body.Network) != nil || !owners[c.Output.Recipient.Owner] {
			return ErrAuth
		}
		if c.Instance == 1 && t.Body.Inputs[i].Kind != FinalInput {
			return ErrAuth
		}
		if t.Body.Kind == FastTransfer && (c.Output.Recipient.Route.Kind != OrgRoute || c.Output.Recipient.Route.Org != t.Body.Certifier) {
			return ErrAuth
		}
		if t.Funding[i].Kind > ReserveFunding || t.Funding[i].Ref == (Hash{}) {
			return ErrRule
		}
	}
	return nil
}

func (t FastTx) VerifyInitial(key *chameleon.Public) error {
	if err := t.VerifyAuth(); err != nil {
		return err
	}
	for i, f := range t.Funding {
		if f.Kind != OriginalFunding || f.Ref != Hash(t.Body.Inputs[i].Output) || !key.Verify(t.FundingContext(i, key.KeyID()), f.ReferenceBytes(), t.Commitments[i], f.Opening) {
			return ErrAuth
		}
	}
	return nil
}

func (t FastTx) MarshalBinary() ([]byte, error) {
	if err := t.VerifyAuth(); err != nil {
		return nil, err
	}
	fixed := new(Encoder)
	fixed.Bytes(t.core())
	for _, c := range t.Commitments {
		fixed.Fixed(c[:])
	}
	fixed.U32(uint32(len(t.Auth)))
	for _, a := range t.Auth {
		fixed.Fixed(a.Owner[:])
		fixed.Fixed(a.Signature[:])
	}
	mutable := new(Encoder)
	for _, f := range t.Funding {
		mutable.Fixed(f.ReferenceBytes())
		mutable.Fixed(f.Opening[:])
	}
	return encodeDirectEnvelope(fixed.Data(), mutable.Data())
}

func encodeDirectEnvelope(fixed, mutable []byte) ([]byte, error) {
	if len(fixed)+len(mutable)+16 > MaxRequestBytes {
		return nil, ErrEncoding
	}
	b := make([]byte, 16+len(fixed)+len(mutable))
	copy(b, directMagic)
	binary.BigEndian.PutUint32(b[8:12], uint32(len(fixed)))
	binary.BigEndian.PutUint32(b[12:16], uint32(len(mutable)))
	copy(b[16:], fixed)
	copy(b[16+len(fixed):], mutable)
	return b, nil
}

func decodeDirectEnvelope(b []byte) ([]byte, []byte, error) {
	if len(b) < 16 || len(b) > MaxRequestBytes || !bytes.Equal(b[:8], directMagic) {
		return nil, nil, ErrEncoding
	}
	n, m := uint64(binary.BigEndian.Uint32(b[8:12])), uint64(binary.BigEndian.Uint32(b[12:16]))
	if 16+n+m != uint64(len(b)) {
		return nil, nil, ErrEncoding
	}
	return b[16 : 16+n], b[16+n:], nil
}

func DecodeFastTx(b []byte) (t FastTx, err error) {
	fixed, mutable, err := decodeDirectEnvelope(b)
	if err != nil {
		return t, err
	}
	d := NewDecoder(fixed)
	core := NewDecoder(d.Bytes(MaxTxBytes))
	t.Body, err = decodeTxVersion(core.Bytes(MaxTxBytes), 3, 3)
	if err != nil {
		return t, err
	}
	t.Claims = make([]InputClaim, core.Count(MaxInputs))
	for i := range t.Claims {
		t.Claims[i] = InputClaim{Output: decodeOutput(core), Instance: core.U8()}
	}
	if err = core.Done(); err != nil {
		return t, err
	}
	t.Commitments = make([]chameleon.Commitment, len(t.Claims))
	for i := range t.Commitments {
		copy(t.Commitments[i][:], d.Fixed(chameleon.Size))
	}
	t.Auth = make([]OwnerAuth, d.Count(MaxInputs))
	for i := range t.Auth {
		copy(t.Auth[i].Owner[:], d.Fixed(32))
		copy(t.Auth[i].Signature[:], d.Fixed(64))
	}
	if err = d.Done(); err != nil {
		return t, err
	}
	f := NewDecoder(mutable)
	t.Funding = make([]Funding, len(t.Claims))
	for i := range t.Funding {
		t.Funding[i].Kind = f.U8()
		copy(t.Funding[i].Ref[:], f.Fixed(32))
		copy(t.Funding[i].Opening[:], f.Fixed(chameleon.Size))
	}
	if err = f.Done(); err != nil {
		return t, err
	}
	return t, t.VerifyAuth()
}

// OutputCertificate is a detached QC summary, never a recursive transaction.
type OutputCommitment struct {
	Digest Hash
	Amount uint64
}
type OutputSummary struct {
	Network        Hash
	Tx             TxID
	Issuer, Config Hash
	Epoch          uint64
	Rules          RuleIDs
	Outputs        []OutputCommitment
	Admission      AdmissionVector
	Grants         []Hash
}
type OutputCertificate struct {
	Summary OutputSummary
	QC      SpendQC
}

func (s OutputSummary) encode() []byte {
	e := new(Encoder)
	e.U64(3)
	e.Fixed(s.Network[:])
	e.Fixed(s.Tx[:])
	e.Fixed(s.Issuer[:])
	e.Fixed(s.Config[:])
	e.U64(s.Epoch)
	s.Rules.encode(e)
	e.U32(uint32(len(s.Outputs)))
	for _, o := range s.Outputs {
		e.Fixed(o.Digest[:])
		e.U64(o.Amount)
	}
	e.Bytes(s.Admission.Encode())
	e.U32(uint32(len(s.Grants)))
	for _, g := range s.Grants {
		e.Fixed(g[:])
	}
	return e.Data()
}
func (s OutputSummary) Fact() SpendFactID              { return SpendFactID(Digest("DIRECT_CERT_V3", s.encode())) }
func (s OutputSummary) OutputID(index uint32) OutputID { return OutputIdentity(s.Network, s.Tx, index) }

func SummaryFor(t FastTx, vector AdmissionVector) OutputSummary {
	s := OutputSummary{Network: t.Body.Network, Tx: t.ID(), Issuer: t.Body.Certifier, Config: t.Body.Config, Epoch: t.Body.Epoch, Rules: t.Body.Rules, Admission: vector}
	for _, o := range t.Body.Outputs {
		s.Outputs = append(s.Outputs, OutputCommitment{Digest: OutputDigest(o), Amount: o.Amount})
	}
	for _, g := range t.Body.Admission {
		s.Grants = append(s.Grants, g.Grant)
	}
	return s
}

func (s OutputSummary) Validate() error {
	if s.Network == (Hash{}) || s.Tx == (TxID{}) || s.Issuer == (Hash{}) || s.Config == (Hash{}) || s.Epoch == 0 || len(s.Outputs) == 0 || len(s.Outputs) > MaxOutputs || len(s.Grants) != len(s.Admission) || s.Rules == (RuleIDs{}) {
		return ErrRule
	}
	if err := s.Admission.Validate(); err != nil {
		return err
	}
	var total uint64
	for _, o := range s.Outputs {
		if o.Amount == 0 || o.Digest == (Hash{}) {
			return ErrRule
		}
		var err error
		total, err = Add(total, o.Amount)
		if err != nil {
			return err
		}
	}
	cal := false
	for i, a := range s.Admission {
		if s.Grants[i] == (Hash{}) {
			return ErrRule
		}
		if a.Key.Kind == ResourceCAL {
			if cal || a.Key.Account != s.Issuer || a.Cap != total {
				return ErrRule
			}
			cal = true
		}
	}
	if !cal {
		return ErrRule
	}
	return nil
}
func (c OutputCertificate) Verify(cfg OrgConfig) error {
	s := c.Summary
	if err := s.Validate(); err != nil {
		return err
	}
	if s.Network != cfg.Network || s.Config != cfg.Hash() || s.Issuer != cfg.Org || s.Epoch != cfg.Epoch || c.QC.Fact != s.Fact() {
		return ErrAuth
	}
	return VerifyQC(c.QC, cfg)
}
func (c OutputCertificate) VerifyOutput(cfg OrgConfig, index uint32, o Output) error {
	if err := c.Verify(cfg); err != nil {
		return err
	}
	if int(index) >= len(c.Summary.Outputs) || o.Asset != AssetCAL || o.Recipient.Verify(c.Summary.Network) != nil || c.Summary.Outputs[index] != (OutputCommitment{Digest: OutputDigest(o), Amount: o.Amount}) {
		return ErrAuth
	}
	return nil
}
func (c OutputCertificate) MarshalBinary() ([]byte, error) {
	if err := c.Summary.Validate(); err != nil {
		return nil, err
	}
	if len(c.QC.Votes) < 3 || len(c.QC.Votes) > 4 || c.QC.Fact != c.Summary.Fact() {
		return nil, ErrAuth
	}
	e := new(Encoder)
	e.U16(304)
	e.Bytes(c.Summary.encode())
	e.Fixed(c.QC.Fact[:])
	e.U32(uint32(len(c.QC.Votes)))
	for _, v := range c.QC.Votes {
		e.U16(v.Member)
		e.Fixed(v.Signature[:])
	}
	return e.Data(), nil
}
func DecodeOutputCertificate(b []byte) (c OutputCertificate, err error) {
	if len(b) > MaxCertificateBytes {
		return c, ErrEncoding
	}
	d := NewDecoder(b)
	if d.U16() != 304 {
		return c, ErrEncoding
	}
	s := NewDecoder(d.Bytes(MaxCertificateBytes))
	if s.U64() != 3 {
		return c, ErrEncoding
	}
	copy(c.Summary.Network[:], s.Fixed(32))
	copy(c.Summary.Tx[:], s.Fixed(32))
	copy(c.Summary.Issuer[:], s.Fixed(32))
	copy(c.Summary.Config[:], s.Fixed(32))
	c.Summary.Epoch = s.U64()
	c.Summary.Rules = decodeRules(s)
	c.Summary.Outputs = make([]OutputCommitment, s.Count(MaxOutputs))
	for i := range c.Summary.Outputs {
		copy(c.Summary.Outputs[i].Digest[:], s.Fixed(32))
		c.Summary.Outputs[i].Amount = s.U64()
	}
	v := NewDecoder(s.Bytes(4096))
	c.Summary.Admission = make(AdmissionVector, v.Count(MaxAdmission))
	for i := range c.Summary.Admission {
		c.Summary.Admission[i] = Allocation{Key: decodeResource(v), Cap: v.U64()}
	}
	if err = v.Done(); err != nil {
		return c, err
	}
	c.Summary.Grants = make([]Hash, s.Count(MaxAdmission))
	for i := range c.Summary.Grants {
		copy(c.Summary.Grants[i][:], s.Fixed(32))
	}
	if err = s.Done(); err != nil {
		return c, err
	}
	copy(c.QC.Fact[:], d.Fixed(32))
	c.QC.Votes = make([]SpendVote, d.Count(4))
	for i := range c.QC.Votes {
		c.QC.Votes[i].Member = d.U16()
		copy(c.QC.Votes[i].Signature[:], d.Fixed(64))
	}
	if err = d.Done(); err != nil {
		return c, err
	}
	return c, c.Summary.Validate()
}
