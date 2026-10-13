package protocol

import "math"

const C3Version uint64 = 1
const C3SummaryVersion uint64 = 6
const MaxFeeCommitmentBytes uint32 = 1024

// FeeResources is the signed bounded work description available even when the
// source transaction is absent. Members must compare it to FeeResourcesFor.
type FeeResources struct {
	Inputs, FeeInputs, Outputs, Admissions uint32
	Work                                   WorkLimit
}

func FeeResourcesFor(t TxBody) FeeResources {
	return FeeResources{uint32(len(t.Inputs)), uint32(len(t.Fee.Inputs)), uint32(len(t.Outputs)), uint32(len(t.Admission)), t.Work}
}
func (r FeeResources) Validate() error {
	if r.Inputs == 0 || r.Inputs > MaxInputs || r.FeeInputs > MaxInputs || uint64(r.Inputs)+uint64(r.FeeInputs) > MaxInputs || r.Outputs == 0 || r.Outputs > MaxOutputs || r.Admissions > MaxAdmission || r.Work.Execution == 0 || r.Work.Bytes == 0 || r.Work.Depth == 0 || r.Work.Ancestors == 0 {
		return ErrRule
	}
	return nil
}

// FeeSchedule prices finite immutable work. Register rates cover verification
// and storage bounds; per-output stages and fixed rewards never grow with delay.
type FeeSchedule struct {
	AttachReward, ExecuteReward, ResolveReward                         uint64
	Register, RegisterPerInput, RegisterPerOutput, RegisterPerResource uint64
	RegisterPerExecution, RegisterPerByte                              uint64
	Open, Close, Compensate, Repay                                     uint64
}

// FeeCommitment is a value so copying cached transactions cannot alias a plan.
// Version zero is allowed only for the completely zero (legacy) value.
// Sponsor identifies the explicitly authorized OrgReserve payer; otherwise
// Payer identifies the owner of the final FUEL inputs. Revenue is an operating
// account and must differ from the protected Pool.
type FeeCommitment struct {
	Version                       uint64
	Policy, Issuer, Pool, Grant   Hash
	GrantVersion                  uint64
	Quote                         uint64
	Payer                         PublicKey
	Sponsor, Revenue, Beneficiary Hash
	Resources                     FeeResources
	Schedule                      FeeSchedule
	Budget                        uint64
}

func (c FeeCommitment) Enabled() bool { return c.Version != 0 }
func (c FeeCommitment) Funding() AdmissionRef {
	return AdmissionRef{Key: ResourceKey{Kind: ResourceFUEL, Account: c.Pool, Version: c.GrantVersion}, Grant: c.Grant}
}

type C3FeePlan struct {
	Maximum, RewardCeiling                     uint64
	AttachReward, ExecuteReward, ResolveReward uint64
	Register, Open, Close, Compensate, Repay   uint64
	Outputs                                    uint32
}

func c3Sum(values ...uint64) (uint64, error) {
	var n uint64
	for _, v := range values {
		var e error
		n, e = Add(n, v)
		if e != nil {
			return 0, e
		}
	}
	return n, nil
}
func c3Mul(a, b uint64) (uint64, error) {
	if b != 0 && a > math.MaxUint64/b {
		return 0, ErrRule
	}
	return a * b, nil
}
func (c FeeCommitment) Plan() (C3FeePlan, error) {
	r, s := c.Resources, c.Schedule
	if e := r.Validate(); e != nil {
		return C3FeePlan{}, e
	}
	p := C3FeePlan{AttachReward: s.AttachReward, ExecuteReward: s.ExecuteReward, ResolveReward: s.ResolveReward, Register: s.Register, Open: s.Open, Close: s.Close, Compensate: s.Compensate, Repay: s.Repay, Outputs: r.Outputs}
	for _, x := range [][2]uint64{{s.RegisterPerInput, uint64(r.Inputs) + uint64(r.FeeInputs)}, {s.RegisterPerOutput, uint64(r.Outputs)}, {s.RegisterPerResource, uint64(r.Admissions)}, {s.RegisterPerExecution, uint64(r.Work.Execution)}, {s.RegisterPerByte, uint64(r.Work.Bytes)}} {
		cost, e := c3Mul(x[0], x[1])
		if e != nil {
			return C3FeePlan{}, e
		}
		p.Register, e = Add(p.Register, cost)
		if e != nil {
			return C3FeePlan{}, e
		}
	}
	resolved, e := c3Mul(s.ResolveReward, uint64(r.Outputs))
	if e != nil {
		return C3FeePlan{}, e
	}
	p.RewardCeiling, e = c3Sum(s.AttachReward, s.ExecuteReward, resolved)
	if e != nil {
		return C3FeePlan{}, e
	}
	stages, e := c3Sum(s.Open, s.Close, s.Compensate, s.Repay)
	if e != nil {
		return C3FeePlan{}, e
	}
	stages, e = c3Mul(stages, uint64(r.Outputs))
	if e != nil {
		return C3FeePlan{}, e
	}
	p.Maximum, e = c3Sum(p.RewardCeiling, p.Register, stages)
	return p, e
}
func (c FeeCommitment) Validate() error {
	if c.Version != C3Version {
		return ErrUnsupported
	}
	if c.Policy == (Hash{}) || c.Issuer == (Hash{}) || c.Pool == (Hash{}) || c.Grant == (Hash{}) || c.GrantVersion == 0 || c.Quote == 0 || c.Revenue == (Hash{}) || c.Revenue == c.Pool || c.Sponsor == c.Pool || c.Beneficiary == c.Pool || c.Beneficiary == (Hash{}) || (c.Payer == (PublicKey{})) == (c.Sponsor == (Hash{})) {
		return ErrRule
	}
	p, e := c.Plan()
	if e != nil {
		return e
	}
	if p.Maximum == 0 || p.Maximum != c.Budget {
		return ErrRule
	}
	return nil
}
func (c FeeCommitment) validateBody(t TxBody) error {
	if c == (FeeCommitment{}) {
		return nil
	}
	if e := c.Validate(); e != nil {
		return e
	}
	if t.Wire != DirectWireVersion || t.Version != DirectVersion || t.Kind != FastTransfer || c.Issuer != t.Certifier || c.Quote != t.Fee.Maximum || c.Resources != FeeResourcesFor(t) {
		return ErrRule
	}
	if t.Fee.Source == OwnerFinalUTXO {
		if c.Payer != t.Fee.Refund.Owner || c.Sponsor != (Hash{}) {
			return ErrAuth
		}
	} else if t.Fee.Source == OrgReserve {
		if c.Sponsor != t.Fee.Account || c.Payer != (PublicKey{}) {
			return ErrAuth
		}
	} else {
		return ErrUnsupported
	}
	return nil
}
func (c FeeCommitment) encode(e *Encoder) {
	e.U64(c.Version)
	e.Fixed(c.Policy[:])
	e.Fixed(c.Issuer[:])
	e.Fixed(c.Pool[:])
	e.Fixed(c.Grant[:])
	e.U64(c.GrantVersion)
	e.U64(c.Quote)
	e.Fixed(c.Payer[:])
	e.Fixed(c.Sponsor[:])
	e.Fixed(c.Revenue[:])
	e.Fixed(c.Beneficiary[:])
	r := c.Resources
	e.U32(r.Inputs)
	e.U32(r.FeeInputs)
	e.U32(r.Outputs)
	e.U32(r.Admissions)
	e.U64(uint64(r.Work.Execution))
	e.U64(uint64(r.Work.Bytes))
	e.U32(r.Work.Depth)
	e.U32(r.Work.Ancestors)
	s := c.Schedule
	for _, v := range []uint64{s.AttachReward, s.ExecuteReward, s.ResolveReward, s.Register, s.RegisterPerInput, s.RegisterPerOutput, s.RegisterPerResource, s.RegisterPerExecution, s.RegisterPerByte, s.Open, s.Close, s.Compensate, s.Repay} {
		e.U64(v)
	}
	e.U64(c.Budget)
}
func (c FeeCommitment) MarshalBinary() ([]byte, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	e := new(Encoder)
	c.encode(e)
	return e.Data(), nil
}
func DecodeFeeCommitment(raw []byte) (c FeeCommitment, err error) {
	if len(raw) > int(MaxFeeCommitmentBytes) {
		return c, ErrEncoding
	}
	d := NewDecoder(raw)
	c.Version = d.U64()
	copy(c.Policy[:], d.Fixed(32))
	copy(c.Issuer[:], d.Fixed(32))
	copy(c.Pool[:], d.Fixed(32))
	copy(c.Grant[:], d.Fixed(32))
	c.GrantVersion = d.U64()
	c.Quote = d.U64()
	copy(c.Payer[:], d.Fixed(32))
	copy(c.Sponsor[:], d.Fixed(32))
	copy(c.Revenue[:], d.Fixed(32))
	copy(c.Beneficiary[:], d.Fixed(32))
	c.Resources = FeeResources{Inputs: d.U32(), FeeInputs: d.U32(), Outputs: d.U32(), Admissions: d.U32(), Work: WorkLimit{ExecUnits(d.U64()), RetainedBytes(d.U64()), d.U32(), d.U32()}}
	c.Schedule = FeeSchedule{d.U64(), d.U64(), d.U64(), d.U64(), d.U64(), d.U64(), d.U64(), d.U64(), d.U64(), d.U64(), d.U64(), d.U64(), d.U64()}
	c.Budget = d.U64()
	if e := d.Done(); e != nil {
		return c, e
	}
	return c, c.Validate()
}
