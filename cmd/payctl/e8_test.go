package main

import (
	"testing"
	"time"
)

func TestE8SlotsDoNotCatchUpInBursts(t *testing.T) {
	start := time.Unix(100, 0)
	due, skipped := e8NextSlot(start, start.Add(105*time.Millisecond), 10*time.Millisecond)
	if !due.Equal(start.Add(110*time.Millisecond)) || skipped != 10 {
		t.Fatalf("%v %d", due, skipped)
	}
}
