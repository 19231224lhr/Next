package main

import (
	"context"
	"testing"
	"time"
)

func TestDirectPacingAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	jobs := directBenchJobs(ctx, 4, 50, start)
	for i := 0; i < 4; i++ {
		select {
		case n, ok := <-jobs:
			if !ok || n != i || time.Since(start) < time.Duration(i)*20*time.Millisecond {
				t.Fatal("out of order or early release", n)
			}
		case <-time.After(time.Second):
			t.Fatal("stalled pacing")
		}
	}
	if _, ok := <-jobs; ok {
		t.Fatal("jobs not closed")
	}
	blocked := directBenchJobs(ctx, 10, 0, time.Now())
	cancel()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("cancel did not unblock producer")
	}
}
