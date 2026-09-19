package rules

import (
	"testing"
	"utxo/protocol"
)

func TestBlockSettlementProducesNoPrivateReceipts(t *testing.T) {
	f := newDirectFixture(t)
	p := f.payment(t, f.genesis, nil, 1)
	tr := f.settle(t, p, 100)
	if len(tr.Facts) != 0 {
		t.Fatalf("normal settlement still manufactures %d proof facts", len(tr.Facts))
	}
	result, err := protocol.DecodeExecution(tr.Data)
	if err != nil || !result.Applied || len(result.MissingInputs) != 0 || len(result.LateOutputs) != 0 {
		t.Fatalf("wrong execution outcome: %+v %v", result, err)
	}
	if len(f.settle(t, p, 101).Data) != 0 {
		t.Fatal("duplicate transaction emitted another execution outcome")
	}
}
