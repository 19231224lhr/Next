package rules

import (
	"testing"
	"utxo/protocol"
)

func TestDirectCreditsRequireEveryDistinctOriginalCap(t *testing.T) {
	f := newDirectFixture(t)
	payment := f.payment(t, f.genesis, nil, 77)
	transition := f.settle(t, payment, 100)
	var facts []protocol.FinalFact
	for _, fact := range transition.Facts {
		if fact.Kind == protocol.FactCredit {
			facts = append(facts, fact)
		}
	}
	if done, err := CheckDirectCredits(payment.Certificate.Summary, facts); err != nil || !done {
		t.Fatalf("complete credits: %v %v", done, err)
	}
	if done, err := CheckDirectCredits(payment.Certificate.Summary, facts[:1]); err != nil || done {
		t.Fatal("partial set became complete")
	}
	duplicate := append(append([]protocol.FinalFact{}, facts...), facts[0])
	if _, err := CheckDirectCredits(payment.Certificate.Summary, duplicate); err == nil {
		t.Fatal("duplicate receipt accepted")
	}
	wrong := append([]protocol.FinalFact{}, facts...)
	wrong[0].Network = protocol.Digest("foreign")
	if _, err := CheckDirectCredits(payment.Certificate.Summary, wrong); err == nil {
		t.Fatal("foreign receipt accepted")
	}
}
