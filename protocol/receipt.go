package protocol

// CreditReceipt is cumulative and bound to the original approval and resource.
// A receipt never authorizes an input unlock or a second first-time approval.
type CreditReceipt struct {
	Spend                                                     SpendFactID
	Resource                                                  ResourceKey
	Original, Paid, Discharged, Returned, Remaining, Revision uint64
}

func (c CreditReceipt) Validate() error {
	if c.Spend == (SpendFactID{}) || c.Resource.Kind < ResourceCAL || c.Resource.Kind > ResourcePolicy || c.Resource.Version == 0 || c.Original == 0 || c.Revision == 0 || c.Returned > c.Paid {
		return ErrRule
	}
	sum, e := Add(c.Paid, c.Discharged)
	if e != nil {
		return e
	}
	sum, e = Add(sum, c.Remaining)
	if e != nil {
		return e
	}
	if sum != c.Original {
		return ErrRule
	}
	return nil
}
func (c CreditReceipt) Key() Hash { return Digest("CREDIT", c.Spend[:], c.Resource.Encode()) }
func (c CreditReceipt) MarshalBinary() ([]byte, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	x := new(Encoder)
	x.U16(51)
	x.U64(WireVersion)
	x.Fixed(c.Spend[:])
	x.Fixed(c.Resource.Encode())
	for _, v := range []uint64{c.Original, c.Paid, c.Discharged, c.Returned, c.Remaining, c.Revision} {
		x.U64(v)
	}
	return x.Data(), nil
}
func DecodeCredit(b []byte) (c CreditReceipt, err error) {
	d := NewDecoder(b)
	if d.U16() != 51 || d.U64() != WireVersion {
		return c, ErrEncoding
	}
	copy(c.Spend[:], d.Fixed(32))
	c.Resource.Kind = ResourceKind(d.U8())
	copy(c.Resource.Account[:], d.Fixed(32))
	c.Resource.Version = d.U64()
	c.Original = d.U64()
	c.Paid = d.U64()
	c.Discharged = d.U64()
	c.Returned = d.U64()
	c.Remaining = d.U64()
	c.Revision = d.U64()
	if err = d.Done(); err != nil {
		return
	}
	err = c.Validate()
	return
}
