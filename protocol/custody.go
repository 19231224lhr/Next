package protocol

// CustodyReceipt binds the permission to the exact committed certificate object
// and certified effects. Local retention replacement is a separate atomic step.
type CustodyReceipt struct {
	Credit               CreditReceipt
	Certificate, Effects Hash
}

func (c CustodyReceipt) MarshalBinary() ([]byte, error) {
	if c.Credit.Resource.Kind != ResourceBytes || c.Certificate == (Hash{}) || c.Effects == (Hash{}) {
		return nil, ErrRule
	}
	raw, e := c.Credit.MarshalBinary()
	if e != nil {
		return nil, e
	}
	enc := new(Encoder)
	enc.U16(53)
	enc.U64(WireVersion)
	enc.Bytes(raw)
	enc.Fixed(c.Certificate[:])
	enc.Fixed(c.Effects[:])
	return enc.Data(), nil
}
func DecodeCustody(raw []byte) (c CustodyReceipt, err error) {
	d := NewDecoder(raw)
	if d.U16() != 53 || d.U64() != WireVersion {
		return c, ErrEncoding
	}
	c.Credit, err = DecodeCredit(d.Bytes(256))
	if err != nil {
		return
	}
	copy(c.Certificate[:], d.Fixed(32))
	copy(c.Effects[:], d.Fixed(32))
	if err = d.Done(); err != nil {
		return
	}
	_, err = c.MarshalBinary()
	return
}
