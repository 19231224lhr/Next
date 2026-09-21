package main

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestDirectPacerBoundsCatchupAfterStall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wait := newDirectPacer(500)
		ctx := context.Background()
		start := time.Now()
		for range 20 {
			if e := wait(ctx); e != nil {
				t.Fatal(e)
			}
		}
		if d := time.Since(start); d < 32*time.Millisecond {
			t.Fatal("initial burst unbounded", d)
		}
		time.Sleep(time.Second)
		start = time.Now()
		var wg sync.WaitGroup
		at := make(chan time.Time, 20)
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if e := wait(ctx); e != nil {
					t.Error(e)
				}
				at <- time.Now()
			}()
		}
		wg.Wait()
		close(at)
		immediate := 0
		for ts := range at {
			if ts.Equal(start) {
				immediate++
			}
		}
		if immediate > 4 || time.Since(start) < 32*time.Millisecond {
			t.Fatal("stall accumulated send debt", immediate, time.Since(start))
		}
		ctx2, cancel := context.WithCancel(ctx)
		cancel()
		before := time.Now()
		if wait(ctx2) == nil || time.Now() != before {
			t.Fatal("cancellation ignored")
		}
	})
}
func TestDirectPacerZeroRate(t *testing.T) {
	wait := newDirectPacer(0)
	if e := wait(context.Background()); e != nil {
		t.Fatal(e)
	}
}
