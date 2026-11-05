package protocol

import (
	"errors"
	"math/bits"
)

type CAL uint64
type FUEL uint64
type ExecUnits uint64
type RetainedBytes uint64
type PolicyUnits uint64

var ErrAmount = errors.New("invalid or overflowing amount")

func Add(a, b uint64) (uint64, error) {
	v, c := bits.Add64(a, b, 0)
	if c != 0 {
		return 0, ErrAmount
	}
	return v, nil
}
func Sub(a, b uint64) (uint64, error) {
	v, c := bits.Sub64(a, b, 0)
	if c != 0 {
		return 0, ErrAmount
	}
	return v, nil
}
func MulDiv(a, b, divisor uint64) (uint64, error) {
	hi, lo := bits.Mul64(a, b)
	if divisor == 0 || hi >= divisor {
		return 0, ErrAmount
	}
	q, _ := bits.Div64(hi, lo, divisor)
	return q, nil
}

// GrantShare rounds each independent grant down before accumulation.
func GrantShare(coverage uint64) (uint64, error) { return MulDiv(coverage, 2, 3) }
