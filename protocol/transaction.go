package protocol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
)

const (
	MaxTxBytes   = 64 * 1024
	MaxInputs    = 16
	MaxOutputs   = 32
	MaxAdmission = 32
)

type TxKind uint8

const (
	FastTransfer   TxKind = 1
	DirectTransfer TxKind = 2
)

type InputKind uint8

const (
	FinalInput       InputKind = 1
	CertificateInput InputKind = 2
)

type Asset uint8

const (
	AssetCAL  Asset = 1
	AssetFUEL Asset = 2
)

type Input struct {
	Kind     InputKind
	Output   OutputID
	Evidence Hash
}
type Output struct {
	Asset     Asset
	Amount    uint64
	Recipient ReceiveDescriptor
}
type FeeSource uint8

const (
	OrgReserve     FeeSource = 1
	OwnerFinalUTXO FeeSource = 2
)

type FeeTerms struct {
	Source          FeeSource
	Account, Policy Hash
	Version         uint64
	Maximum         uint64
	Inputs          []Input
	Refund          ReceiveDescriptor
}
type WorkLimit struct {
	Execution ExecUnits
	Bytes     RetainedBytes
	Depth     uint32
	Ancestors uint32
}
type ResourceKind uint8

const (
	ResourceCAL       ResourceKind = 1
	ResourceFUEL      ResourceKind = 2
	ResourceExecution ResourceKind = 3
	ResourceBytes     ResourceKind = 4
	ResourcePolicy    ResourceKind = 5
)

type ResourceKey struct {
	Kind    ResourceKind
	Account Hash
	Version uint64
}
type AdmissionRef struct {
	Key   ResourceKey
	Grant Hash
}
type Allocation struct {
	Key ResourceKey
	Cap uint64
}
type AdmissionVector []Allocation

func (k ResourceKey) Encode() []byte {
	e := new(Encoder)
	e.U8(uint8(k.Kind))
	e.Fixed(k.Account[:])
	e.U64(k.Version)
	return e.Data()
}
func decodeResource(d *Decoder) (k ResourceKey) {
	k.Kind = ResourceKind(d.U8())
	copy(k.Account[:], d.Fixed(32))
	k.Version = d.U64()
	return
}
func (v AdmissionVector) Validate() error {
	if len(v) > MaxAdmission {
		return ErrRule
	}
	for i, x := range v {
		if x.Key.Kind < ResourceCAL || x.Key.Kind > ResourcePolicy || x.Cap == 0 || x.Key.Version == 0 || x.Key.Account == (Hash{}) {
			return ErrRule
		}
		if i > 0 && bytes.Compare(v[i-1].Key.Encode(), x.Key.Encode()) >= 0 {
			return ErrRule
		}
	}
	return nil
}
func (v AdmissionVector) Encode() []byte {
	e := new(Encoder)
	e.U32(uint32(len(v)))
	for _, x := range v {
		e.Fixed(x.Key.Encode())
		e.U64(x.Cap)
	}
	return e.Data()
}

type TxBody struct {
	Wire, Version uint64
	Network       Hash
	Kind          TxKind
	Intent        Hash
	Subject       PublicKey
	Nonce         Nonce
	Certifier     Hash
	Epoch         uint64
	Config        Hash
	Rules         RuleIDs
	Inputs        []Input
	Outputs       []Output
	Fee           FeeTerms
	Admission     []AdmissionRef
	Work          WorkLimit
}

func (t TxBody) IntentID() Hash { return Digest("INTENT", t.Network[:], t.Subject[:], t.Nonce[:]) }
func (t TxBody) Validate() error {
	if t.Wire != WireVersion || t.Version != ProtocolVersion {
		return ErrUnsupported
	}
	if t.Network == (Hash{}) || t.Intent != t.IntentID() || t.Subject == (PublicKey{}) || t.Rules.Fee == (Hash{}) || t.Rules.Work == (Hash{}) || t.Rules.Accounting == (Hash{}) {
		return ErrRule
	}
	if len(t.Inputs) == 0 || len(t.Inputs) > MaxInputs || len(t.Outputs) == 0 || len(t.Outputs) > MaxOutputs || len(t.Fee.Inputs) > MaxInputs || len(t.Admission) > MaxAdmission || len(t.Inputs)+len(t.Fee.Inputs) > MaxInputs {
		return ErrRule
	}
	if t.Kind == FastTransfer {
		if t.Certifier == (Hash{}) || t.Config == (Hash{}) || t.Epoch == 0 || t.Work.Execution == 0 || t.Work.Bytes == 0 || t.Work.Depth == 0 || t.Work.Ancestors == 0 {
			return ErrRule
		}
	} else if t.Kind == DirectTransfer {
		if t.Certifier != (Hash{}) || t.Config != (Hash{}) || t.Epoch != 0 || len(t.Admission) != 0 || t.Work != (WorkLimit{}) || t.Fee.Source != OwnerFinalUTXO {
			return ErrRule
		}
	} else {
		return ErrUnsupported
	}
	seen := make(map[OutputID]bool)
	for _, group := range [][]Input{t.Inputs, t.Fee.Inputs} {
		for i, in := range group {
			if in.Kind != FinalInput && in.Kind != CertificateInput || in.Output == (OutputID{}) || in.Evidence == (Hash{}) || seen[in.Output] || i > 0 && bytes.Compare(group[i-1].Output[:], in.Output[:]) >= 0 {
				return ErrRule
			}
			if t.Kind == DirectTransfer && in.Kind != FinalInput {
				return ErrRule
			}
			seen[in.Output] = true
		}
	}
	for _, o := range t.Outputs {
		if o.Asset != AssetCAL || o.Amount == 0 || o.Recipient.Verify(t.Network) != nil {
			return ErrRule
		}
	}
	if t.Fee.Maximum == 0 {
		return ErrRule
	}
	switch t.Fee.Source {
	case OrgReserve:
		if t.Kind != FastTransfer || t.Fee.Account == (Hash{}) || t.Fee.Policy == (Hash{}) || t.Fee.Version == 0 || len(t.Fee.Inputs) != 0 || t.Fee.Refund != (ReceiveDescriptor{}) {
			return ErrRule
		}
	case OwnerFinalUTXO:
		if len(t.Fee.Inputs) == 0 || t.Fee.Account != (Hash{}) || t.Fee.Policy != (Hash{}) || t.Fee.Version != 0 || t.Fee.Refund.Verify(t.Network) != nil {
			return ErrRule
		}
		for _, in := range t.Fee.Inputs {
			if in.Kind != FinalInput {
				return ErrRule
			}
		}
	default:
		return ErrUnsupported
	}
	for i, r := range t.Admission {
		if r.Key.Kind < ResourceCAL || r.Key.Kind > ResourcePolicy || r.Key.Account == (Hash{}) || r.Key.Version == 0 || r.Grant == (Hash{}) || i > 0 && bytes.Compare(t.Admission[i-1].Key.Encode(), r.Key.Encode()) >= 0 {
			return ErrRule
		}
	}
	return nil
}
func encodeInputs(e *Encoder, ins []Input) {
	e.U32(uint32(len(ins)))
	for _, in := range ins {
		e.U8(uint8(in.Kind))
		e.Fixed(in.Output[:])
		e.Fixed(in.Evidence[:])
	}
}
func decodeInputs(d *Decoder) []Input {
	v := make([]Input, d.Count(MaxInputs))
	for i := range v {
		v[i].Kind = InputKind(d.U8())
		copy(v[i].Output[:], d.Fixed(32))
		copy(v[i].Evidence[:], d.Fixed(32))
	}
	return v
}
func (t TxBody) encode() []byte {
	e := new(Encoder)
	e.U16(1)
	e.U64(t.Wire)
	e.U64(t.Version)
	e.Fixed(t.Network[:])
	e.U8(uint8(t.Kind))
	e.Fixed(t.Intent[:])
	e.Fixed(t.Subject[:])
	e.Fixed(t.Nonce[:])
	t.Rules.encode(e)
	if t.Kind == FastTransfer {
		e.Fixed(t.Certifier[:])
		e.U64(t.Epoch)
		e.Fixed(t.Config[:])
	}
	encodeInputs(e, t.Inputs)
	e.U32(uint32(len(t.Outputs)))
	for _, o := range t.Outputs {
		e.U8(uint8(o.Asset))
		e.U64(o.Amount)
		o.Recipient.encode(e)
	}
	e.U8(uint8(t.Fee.Source))
	e.Fixed(t.Fee.Account[:])
	e.Fixed(t.Fee.Policy[:])
	e.U64(t.Fee.Version)
	e.U64(t.Fee.Maximum)
	encodeInputs(e, t.Fee.Inputs)
	if t.Fee.Source == OwnerFinalUTXO {
		t.Fee.Refund.encode(e)
	}
	if t.Kind == FastTransfer {
		e.U32(uint32(len(t.Admission)))
		for _, r := range t.Admission {
			e.Fixed(r.Key.Encode())
			e.Fixed(r.Grant[:])
		}
		e.U64(uint64(t.Work.Execution))
		e.U64(uint64(t.Work.Bytes))
		e.U32(t.Work.Depth)
		e.U32(t.Work.Ancestors)
		e.Optional(false)
	}
	return e.Data()
}
func (t TxBody) MarshalBinary() ([]byte, error) {
	if e := t.Validate(); e != nil {
		return nil, e
	}
	b := t.encode()
	if len(b) > MaxTxBytes {
		return nil, ErrEncoding
	}
	return b, nil
}

// ID is meaningful only after Validate succeeds at the trust boundary.
func (t TxBody) ID() TxID { return TxID(sha256.Sum256(t.encode())) }
func DecodeTx(b []byte) (t TxBody, err error) {
	if len(b) > MaxTxBytes {
		return t, ErrEncoding
	}
	d := NewDecoder(b)
	if d.U16() != 1 {
		return t, ErrEncoding
	}
	t.Wire = d.U64()
	t.Version = d.U64()
	copy(t.Network[:], d.Fixed(32))
	t.Kind = TxKind(d.U8())
	copy(t.Intent[:], d.Fixed(32))
	copy(t.Subject[:], d.Fixed(32))
	copy(t.Nonce[:], d.Fixed(16))
	t.Rules = decodeRules(d)
	if t.Kind == FastTransfer {
		copy(t.Certifier[:], d.Fixed(32))
		t.Epoch = d.U64()
		copy(t.Config[:], d.Fixed(32))
	}
	t.Inputs = decodeInputs(d)
	t.Outputs = make([]Output, d.Count(MaxOutputs))
	for i := range t.Outputs {
		t.Outputs[i].Asset = Asset(d.U8())
		t.Outputs[i].Amount = d.U64()
		t.Outputs[i].Recipient = decodeDescriptor(d)
	}
	t.Fee.Source = FeeSource(d.U8())
	copy(t.Fee.Account[:], d.Fixed(32))
	copy(t.Fee.Policy[:], d.Fixed(32))
	t.Fee.Version = d.U64()
	t.Fee.Maximum = d.U64()
	t.Fee.Inputs = decodeInputs(d)
	if t.Fee.Source == OwnerFinalUTXO {
		t.Fee.Refund = decodeDescriptor(d)
	}
	if t.Kind == FastTransfer {
		t.Admission = make([]AdmissionRef, d.Count(MaxAdmission))
		for i := range t.Admission {
			t.Admission[i].Key = decodeResource(d)
			copy(t.Admission[i].Grant[:], d.Fixed(32))
		}
		t.Work = WorkLimit{ExecUnits(d.U64()), RetainedBytes(d.U64()), d.U32(), d.U32()}
		if d.Optional() {
			return t, ErrUnsupported
		}
	}
	if e := d.Done(); e != nil {
		return t, e
	}
	return t, t.Validate()
}

type OwnerAuth struct {
	Owner     PublicKey
	Signature Signature
}

func SignOwner(tx TxID, key ed25519.PrivateKey) OwnerAuth {
	a := OwnerAuth{}
	copy(a.Owner[:], key.Public().(ed25519.PublicKey))
	h := Digest("OWNER", tx[:])
	copy(a.Signature[:], ed25519.Sign(key, h[:]))
	return a
}
func VerifyOwner(tx TxID, a OwnerAuth) error {
	h := Digest("OWNER", tx[:])
	if !ed25519.Verify(a.Owner[:], h[:], a.Signature[:]) {
		return ErrAuth
	}
	return nil
}

type SignedTx struct {
	Body TxBody
	Auth []OwnerAuth
}
