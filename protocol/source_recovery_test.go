package protocol

import "testing"

func TestRecoveryResultEncodingAndLegacySeparation(t *testing.T) {
	r := ExecutionResult{Applied: true, RecoveredOutputs: []uint32{0, 2}}
	b, err := r.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeExecution(b)
	if err != nil || len(got.RecoveredOutputs) != 2 || got.RecoveredOutputs[1] != 2 {
		t.Fatal("recovery result roundtrip", got, err)
	}
	for _, indices := range [][]uint32{{2, 0}, {0, 0}} {
		r.RecoveredOutputs = indices
		if _, err := r.MarshalBinary(); err == nil {
			t.Fatal("ambiguous recovery indices accepted")
		}
	}
	// Version 410 used the second list for extra user instances. Never
	// silently reinterpret that historical economic rule as reserve recovery.
	b[1] = 154
	if _, err := DecodeExecution(b); err == nil {
		t.Fatal("legacy execution result accepted under changed economics")
	}
}
