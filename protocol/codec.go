package protocol

import (
	"encoding/binary"
	"errors"
)

var ErrEncoding = errors.New("invalid canonical encoding")

// Encoder is only used by explicit schemas; maps and reflection never enter signed bytes.
type Encoder struct{ data []byte }

func (e *Encoder) U8(v uint8)     { e.data = append(e.data, v) }
func (e *Encoder) U16(v uint16)   { e.data = binary.BigEndian.AppendUint16(e.data, v) }
func (e *Encoder) U32(v uint32)   { e.data = binary.BigEndian.AppendUint32(e.data, v) }
func (e *Encoder) U64(v uint64)   { e.data = binary.BigEndian.AppendUint64(e.data, v) }
func (e *Encoder) Fixed(v []byte) { e.data = append(e.data, v...) }
func (e *Encoder) Bytes(v []byte) { e.U32(uint32(len(v))); e.Fixed(v) }
func (e *Encoder) Optional(v bool) {
	if v {
		e.U8(1)
	} else {
		e.U8(0)
	}
}
func (e *Encoder) Data() []byte { return e.data }

type Decoder struct {
	data []byte
	pos  int
	err  error
}

func NewDecoder(b []byte) *Decoder { return &Decoder{data: b} }
func (d *Decoder) take(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n < 0 || n > len(d.data)-d.pos {
		d.err = ErrEncoding
		return nil
	}
	b := d.data[d.pos : d.pos+n]
	d.pos += n
	return b
}
func (d *Decoder) Fixed(n int) []byte { return d.take(n) }
func (d *Decoder) U8() uint8 {
	b := d.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}
func (d *Decoder) U16() uint16 {
	b := d.take(2)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint16(b)
}
func (d *Decoder) U32() uint32 {
	b := d.take(4)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}
func (d *Decoder) U64() uint64 {
	b := d.take(8)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}
func (d *Decoder) Bytes(max uint32) []byte {
	n := d.U32()
	if n > max {
		d.err = ErrEncoding
		return nil
	}
	return d.take(int(n))
}
func (d *Decoder) Count(max uint32) int {
	n := d.U32()
	if n > max {
		d.err = ErrEncoding
		return 0
	}
	return int(n)
}
func (d *Decoder) Optional() bool {
	v := d.U8()
	if v > 1 {
		d.err = ErrEncoding
	}
	return v == 1
}
func (d *Decoder) Done() error {
	if d.err != nil || d.pos != len(d.data) {
		return ErrEncoding
	}
	return nil
}
