package member

import (
	"testing"
	"utxo/protocol"
)

func TestApprovalSummaryCacheOwnsAndBoundsEntries(t *testing.T) {
	_, one := cacheCertificate(1)
	_, two := cacheCertificate(2)
	disabled := &approvalSummaryCache{}
	disabled.put(one.Summary)
	if _, ok := disabled.get(one.Summary.Fact()); ok {
		t.Fatal("zero limit cached")
	}
	cache := &approvalSummaryCache{limit: 1, entries: make(map[protocol.SpendFactID]protocol.OutputSummary)}
	fact := one.Summary.Fact()
	cache.put(one.Summary)
	one.Summary.Outputs[0].Amount++
	got, ok := cache.get(fact)
	if !ok || got.Fact() != fact {
		t.Fatal("caller mutated retained summary")
	}
	got.Outputs[0].Amount++
	got, ok = cache.get(fact)
	if !ok || got.Fact() != fact {
		t.Fatal("getter exposed mutable storage")
	}
	cache.put(two.Summary)
	if _, ok := cache.get(fact); ok {
		t.Fatal("capacity bound ignored")
	}
	if _, ok := cache.get(two.Summary.Fact()); !ok {
		t.Fatal("new summary absent")
	}
}
