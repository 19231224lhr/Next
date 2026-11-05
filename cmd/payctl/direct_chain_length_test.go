package main

import "testing"

func TestValidChainLength(t *testing.T) {
	for _, tc := range []struct {
		length int
		e5     bool
		want   bool
	}{
		{0, false, false}, {1, false, true}, {100, false, true},
		{101, false, true}, {1000, false, true}, {1001, false, false},
		{0, true, false}, {1, true, true}, {100, true, true},
		{101, true, false}, {1000, true, false},
	} {
		if got := validChainLength(tc.length, tc.e5); got != tc.want {
			t.Errorf("length=%d e5=%v: got %v, want %v", tc.length, tc.e5, got, tc.want)
		}
	}
}
