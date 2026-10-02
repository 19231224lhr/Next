package protocol

import (
	"bytes"
	"testing"
	"utxo/crypto/chameleon"
)

func TestDecisionAndRepresentationBindings(t *testing.T) {
	c := CompensationDecision{Network: Hash{1}, Output: OutputID{2}, Height: 3, Transaction: 4, Input: 5}
	raw, err := c.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeCompensationDecision(raw)
	if err != nil || got != c || len(raw) != 88 {
		t.Fatal("decision round trip", err)
	}
	for _, bad := range [][]byte{raw[:87], append(bytes.Clone(raw), 0)} {
		if _, err := DecodeCompensationDecision(bad); err == nil {
			t.Fatal("noncanonical decision")
		}
	}
	effect := RepairEffect{Output: c.Output, ParentFact: SpendFactID{1}, ConsumerFact: SpendFactID{2}, ConsumerTx: TxID{3}, Input: c.Input, Amount: 100, Debit: ReserveDebitIdentity(c.Network, c.Output)}
	data, err := (CompensationResult{Decision: c.ID(), Applied: true, Effects: []RepairEffect{effect}}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	result, effects, err := DecodePublicExecution(c.Network, raw, data)
	if err != nil || !result.Applied || len(effects) != 1 {
		t.Fatal("economic projection", err)
	}
	wrong := c
	wrong.Transaction++
	bad, _ := wrong.MarshalBinary()
	if _, _, err := DecodePublicExecution(c.Network, bad, data); err == nil {
		t.Fatal("result rebound to another slot")
	}
	repair := RepairBatch{Network: c.Network, Height: 3, Items: []RepairItem{{Output: c.Output}}, Parts: []chameleon.Opening{{}}}
	repairRaw, err := repair.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := (RepairResult{Command: repair.ID(), Applied: true}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	result, effects, err = DecodePublicExecution(c.Network, repairRaw, repaired)
	if err != nil || result.Applied || len(effects) != 0 || len(result.FeeOutputs) != 0 {
		t.Fatal("representation leaked effects", err)
	}
	if _, _, err := DecodePublicExecution(c.Network, repairRaw, data); err == nil {
		t.Fatal("economic result on representation")
	}
	if _, _, err := DecodePublicExecution(c.Network, raw, repaired); err == nil {
		t.Fatal("representation result on decision")
	}
	if _, err := DecodeExecution(repaired); err == nil {
		t.Fatal("representation accepted as payment")
	}
	legacy := bytes.Clone(data)
	legacy[0], legacy[1] = 1, 158 // old tag 414
	if _, _, err := DecodePublicExecution(c.Network, raw, legacy); err == nil {
		t.Fatal("old coupled result accepted")
	}
	copy(repairRaw[:8], "RPBATCH4")
	if _, err := DecodeRepairBatch(repairRaw); err == nil {
		t.Fatal("old repair command accepted")
	}
}
