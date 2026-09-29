package protocol

import (
	"bytes"
	"reflect"
	"testing"
	"utxo/crypto/chameleon"
)

func TestRepairBatchCanonicalIdentity(t *testing.T) {
	c := RepairBatch{Network: Hash{1}, Height: 1, Previous: Hash{2}, Next: Hash{3}, Items: []RepairItem{{Output: OutputID{1}, Transaction: 0, Input: 0}, {Output: OutputID{2}, Transaction: 0, Input: 1}}, Parts: []chameleon.Opening{{1}}}
	raw, err := c.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRepairBatch(raw)
	if err != nil || !reflect.DeepEqual(decoded, c) {
		t.Fatal("round trip", err)
	}
	id := c.ID()
	changed := decoded
	changed.Items = append([]RepairItem(nil), c.Items...)
	changed.Items[0].Opening[0] = 1
	if changed.ID() == id {
		t.Fatal("input witness excluded from identity")
	}
	changed = decoded
	changed.Parts = []chameleon.Opening{{2}}
	if changed.ID() == id {
		t.Fatal("part witness excluded from identity")
	}
	for _, items := range [][]RepairItem{nil, {c.Items[1], c.Items[0]}, {c.Items[0], c.Items[0]}, {{Output: OutputID{1}, Input: 0}, {Output: OutputID{1}, Input: 1}}, {{Output: OutputID{1}, Input: 0}, {Output: OutputID{2}, Input: 0}}} {
		bad := c
		bad.Items = items
		if _, err := bad.MarshalBinary(); err == nil {
			t.Fatal("accepted empty, unordered or duplicate batch")
		}
	}
	for i := 0; i < len(raw); i++ {
		if _, err := DecodeRepairBatch(raw[:i]); err == nil {
			t.Fatalf("accepted truncated batch %d", i)
		}
	}
	if _, err := DecodeRepairBatch(append(bytes.Clone(raw), 0)); err == nil {
		t.Fatal("trailing data")
	}
}

func TestRepairBatchResultEffects(t *testing.T) {
	effect := RepairEffect{Output: OutputID{1}, ParentFact: SpendFactID{2}, ConsumerFact: SpendFactID{3}, ConsumerTx: TxID{4}, Amount: 40, Debit: Hash{5}}
	r := RepairBatchResult{Batch: Hash{1}, Applied: true, Effects: []RepairEffect{effect}}
	raw, err := r.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRepairBatchResult(raw)
	if err != nil || !reflect.DeepEqual(got, r) {
		t.Fatal("result round trip", err)
	}
	r.Applied = false
	if _, err := r.MarshalBinary(); err == nil {
		t.Fatal("no-op included economic effects")
	}
	r.Effects = nil
	raw, err = r.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	got, err = DecodeRepairBatchResult(raw)
	if err != nil || got.Applied || len(got.Effects) != 0 {
		t.Fatal("no-op", err)
	}
}
