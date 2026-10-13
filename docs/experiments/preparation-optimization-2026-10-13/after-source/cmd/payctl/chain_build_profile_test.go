package main

import (
	"testing"
	"time"
)

func TestChainBuildTraceMeasuresContiguousStages(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	trace := newChainBuildTrace(true, clock)
	now = now.Add(2 * time.Millisecond)
	trace.mark("read_input")
	now = now.Add(3 * time.Millisecond)
	trace.mark("generate_transaction")
	now = now.Add(time.Millisecond)
	trace.mark("save_request")
	if trace.stages["read_input"] != 2 || trace.stages["generate_transaction"] != 3 || trace.stages["save_request"] != 1 {
		t.Fatalf("incorrect exclusive stage durations: %v", trace.stages)
	}
}

func TestChainBuildTraceDisabledDoesNotReadClock(t *testing.T) {
	trace := newChainBuildTrace(false, func() time.Time { t.Fatal("disabled trace read clock"); return time.Time{} })
	trace.mark("read_input")
	if trace.stages != nil {
		t.Fatalf("disabled trace emitted data: %v", trace.stages)
	}
}
