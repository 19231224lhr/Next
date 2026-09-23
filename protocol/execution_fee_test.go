package protocol

import (
	"bytes"
	"testing"
)

func TestExecutionFeeOutputEncoding(t *testing.T) {
	r := ExecutionResult{Applied: true, FeeOutputs: []FeeOutput{{Transaction: TxID{1}, Index: FeeRefundIndex, Output: Output{Asset: AssetFUEL, Amount: 9}}}}
	raw, e := r.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	if len(raw) != 11+4+int(FeeOutputBytes) {
		t.Fatalf("fee output size %d", len(raw))
	}
	got, e := DecodeExecution(raw)
	if e != nil {
		t.Fatal(e)
	}
	again, e := got.MarshalBinary()
	if e != nil || !bytes.Equal(raw, again) {
		t.Fatal("noncanonical roundtrip", e)
	}
	for _, change := range []func(*ExecutionResult){
		func(r *ExecutionResult) { r.FeeOutputs = append(r.FeeOutputs, r.FeeOutputs[0]) },
		func(r *ExecutionResult) { r.FeeOutputs[0].Index = 0 },
		func(r *ExecutionResult) { r.FeeOutputs[0].Output.Asset = AssetCAL },
		func(r *ExecutionResult) { r.Applied = false },
	} {
		bad := r
		bad.FeeOutputs = append([]FeeOutput(nil), r.FeeOutputs...)
		change(&bad)
		if _, e := bad.MarshalBinary(); e == nil {
			t.Fatal("invalid fee output accepted")
		}
	}
	legacy, _ := (ExecutionResult{Applied: true}).MarshalBinary()
	if legacy[1] != 154 {
		t.Fatal("legacy tag changed")
	}
	if _, e := DecodeExecution(legacy); e != nil {
		t.Fatal(e)
	}
	if _, e := DecodeExecution(append(raw, 0)); e == nil {
		t.Fatal("trailing data accepted")
	}
}
