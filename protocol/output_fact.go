package protocol

// SettledOutput establishes creation only; it makes no claim about current spend state.
type SettledOutput struct {
	Spend       SpendFactID
	ID          OutputID
	Transaction TxID
	Index       uint32
	Output      Output
}

func (s SettledOutput) MarshalBinary() ([]byte, error) {
	if s.ID != OutputIdentity(s.Output.Recipient.Network, s.Transaction, s.Index) || s.Output.Amount == 0 || s.Output.Recipient.Verify(s.Output.Recipient.Network) != nil {
		return nil, ErrRule
	}
	e := new(Encoder)
	e.U16(52)
	e.U64(WireVersion)
	e.Fixed(s.ID[:])
	e.Fixed(s.Transaction[:])
	e.Fixed(s.Spend[:])
	e.U32(s.Index)
	e.U8(uint8(s.Output.Asset))
	e.U64(s.Output.Amount)
	s.Output.Recipient.encode(e)
	return e.Data(), nil
}
func DecodeSettledOutput(b []byte) (s SettledOutput, err error) {
	d := NewDecoder(b)
	if d.U16() != 52 || d.U64() != WireVersion {
		return s, ErrEncoding
	}
	copy(s.ID[:], d.Fixed(32))
	copy(s.Transaction[:], d.Fixed(32))
	copy(s.Spend[:], d.Fixed(32))
	s.Index = d.U32()
	s.Output.Asset = Asset(d.U8())
	s.Output.Amount = d.U64()
	s.Output.Recipient = decodeDescriptor(d)
	if err = d.Done(); err != nil {
		return
	}
	_, err = s.MarshalBinary()
	return
}
