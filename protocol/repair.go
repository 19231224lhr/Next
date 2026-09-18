package protocol

import (
	"bytes"
	"utxo/crypto/chameleon"
)

var repairMagic = []byte{'R', 'E', 'P', 'A', 'I', 'R', '3', 0}

func ClockTick(network Hash, height int64) []byte {
	e := new(Encoder)
	e.Fixed([]byte("CLOCKV3\x00"))
	e.Fixed(network[:])
	e.U64(uint64(height))
	return e.Data()
}
func IsClockTick(raw []byte, network Hash) bool {
	return len(raw) == 48 && bytes.Equal(raw[:8], []byte("CLOCKV3\x00")) && bytes.Equal(raw[8:40], network[:])
}

// RepairInput is immutable. It authenticates the exact next bytes of one input
// and all affected block part openings in the block where compensation occurs.
type RepairInput struct {
	Network          Hash
	Output           OutputID
	Height           int64
	Transaction      uint32
	Input            uint32
	Base             uint64
	Previous, Next   Hash
	TransactionBytes []byte
	Parts            []chameleon.Opening
}

func IsRepairInput(b []byte) bool { return len(b) >= 8 && bytes.Equal(b[:8], repairMagic) }
func (r RepairInput) MarshalBinary() ([]byte, error) {
	if r.Network == (Hash{}) || r.Output == (OutputID{}) || r.Height <= 0 || r.Base == ^uint64(0) || len(r.TransactionBytes) == 0 || len(r.Parts) == 0 || len(r.Parts) > 16384 {
		return nil, ErrRule
	}
	e := new(Encoder)
	e.Fixed(repairMagic)
	e.Fixed(r.Network[:])
	e.Fixed(r.Output[:])
	e.U64(uint64(r.Height))
	e.U32(r.Transaction)
	e.U32(r.Input)
	e.U64(r.Base)
	e.Fixed(r.Previous[:])
	e.Fixed(r.Next[:])
	e.Bytes(r.TransactionBytes)
	e.U32(uint32(len(r.Parts)))
	for _, p := range r.Parts {
		e.Fixed(p[:])
	}
	if len(e.Data()) > MaxRequestBytes {
		return nil, ErrEncoding
	}
	return e.Data(), nil
}
func DecodeRepairInput(b []byte) (r RepairInput, err error) {
	if len(b) > MaxRequestBytes || !IsRepairInput(b) {
		return r, ErrEncoding
	}
	d := NewDecoder(b)
	d.Fixed(8)
	copy(r.Network[:], d.Fixed(32))
	copy(r.Output[:], d.Fixed(32))
	r.Height = int64(d.U64())
	r.Transaction = d.U32()
	r.Input = d.U32()
	r.Base = d.U64()
	copy(r.Previous[:], d.Fixed(32))
	copy(r.Next[:], d.Fixed(32))
	r.TransactionBytes = d.Bytes(MaxRequestBytes)
	r.Parts = make([]chameleon.Opening, d.Count(16384))
	for i := range r.Parts {
		copy(r.Parts[i][:], d.Fixed(chameleon.Size))
	}
	if err = d.Done(); err != nil {
		return r, err
	}
	_, err = r.MarshalBinary()
	return r, err
}
