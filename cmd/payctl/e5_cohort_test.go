package main

import (
	"testing"
	"utxo/protocol"
)

func TestE5AuditExcludesOnlyExplicitlyUnsentRequests(t *testing.T) {
	sent := faultSample{SentNS: 1, Certificate: &protocol.OutputCertificate{}}
	unsent := faultSample{Error: "not sent"}
	got, e := faultAuditSamples([]faultSample{sent, unsent}, true)
	if e != nil || len(got) != 1 {
		t.Fatalf("%d %v", len(got), e)
	}
	if _, e = faultAuditSamples([]faultSample{sent, unsent}, false); e == nil {
		t.Fatal("strict audit silently omitted unsent")
	}
	for _, bad := range []faultSample{{SentNS: 1, Error: "timeout"}, {ReadyNS: 1, Error: "not sent"}, {Error: "unknown"}} {
		if _, e = faultAuditSamples([]faultSample{sent, bad}, true); e == nil {
			t.Fatal("failed or ambiguous request omitted")
		}
	}
}
