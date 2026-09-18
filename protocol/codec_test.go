package protocol

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

func TestCanonicalPrimitiveGolden(t *testing.T) {
	e := new(Encoder)
	e.U8(2)
	e.U16(513)
	e.U32(3)
	e.U64(0x0102030405060708)
	e.Bytes([]byte{0xaa, 0xbb})
	e.Optional(true)
	want, _ := hex.DecodeString("02020100000003010203040506070800000002aabb01")
	if !bytes.Equal(e.Data(), want) {
		t.Fatalf("%x", e.Data())
	}
	d := NewDecoder(want)
	if d.U8() != 2 || d.U16() != 513 || d.U32() != 3 || d.U64() != 0x0102030405060708 || !bytes.Equal(d.Bytes(2), []byte{0xaa, 0xbb}) || !d.Optional() || d.Done() != nil {
		t.Fatal("decode mismatch")
	}
}
func TestDecoderRejectsMalformed(t *testing.T) {
	for _, b := range [][]byte{{}, {0, 0, 0}, {0xff, 0xff, 0xff, 0xff}, {0, 0, 0, 3, 1, 2}} {
		d := NewDecoder(b)
		d.Bytes(2)
		if !errors.Is(d.Done(), ErrEncoding) {
			t.Fatalf("accepted %x", b)
		}
	}
	d := NewDecoder([]byte{2})
	d.Optional()
	if d.Done() == nil {
		t.Fatal("invalid optional")
	}
	d = NewDecoder([]byte{1, 2})
	d.U8()
	if d.Done() == nil {
		t.Fatal("trailing byte")
	}
}
func FuzzDecoder(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 5})
	f.Fuzz(func(t *testing.T, b []byte) { d := NewDecoder(b); d.Bytes(128); d.U64(); d.Optional(); _ = d.Done() })
}
