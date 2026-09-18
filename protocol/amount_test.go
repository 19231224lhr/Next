package protocol

import (
	"math"
	"math/big"
	"testing"
)

func TestC03CheckedAmounts(t *testing.T) {
	for _, x := range [][3]uint64{{0, 0, 0}, {9, 7, 16}, {math.MaxUint64, 0, math.MaxUint64}} {
		v, e := Add(x[0], x[1])
		if e != nil || v != x[2] {
			t.Fatalf("add %v: %d %v", x, v, e)
		}
	}
	if _, e := Add(math.MaxUint64, 1); e == nil {
		t.Fatal("overflow accepted")
	}
	if _, e := Sub(0, 1); e == nil {
		t.Fatal("underflow accepted")
	}
	for _, x := range [][3]uint64{{math.MaxUint64, 2, 3}, {math.MaxUint64, math.MaxUint64, math.MaxUint64}, {90, 2, 3}, {1, 2, 3}} {
		want := new(big.Int).Mul(new(big.Int).SetUint64(x[0]), new(big.Int).SetUint64(x[1]))
		want.Div(want, new(big.Int).SetUint64(x[2]))
		v, e := MulDiv(x[0], x[1], x[2])
		if e != nil || v != want.Uint64() {
			t.Fatalf("muldiv %v: %d %v", x, v, e)
		}
	}
	if _, e := MulDiv(1, 1, 0); e == nil {
		t.Fatal("zero divisor accepted")
	}
	if _, e := MulDiv(math.MaxUint64, math.MaxUint64, 1); e == nil {
		t.Fatal("overflow accepted")
	}
	a, _ := GrantShare(1)
	b, _ := GrantShare(2)
	if a+b != 1 {
		t.Fatal("grant rounding must remain per grant")
	}
}
