package main

import (
	"context"
	"testing"
	"time"
)

func TestDirectDispatchReleasesFastSlotBeforeObservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	jobs := make(chan int, 2)
	jobs <- 0
	jobs <- 1
	close(jobs)
	waits := make([]directDispatchWait, 2)
	firstObserving, secondStarted, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		runDirectBench(ctx, jobs, 1, 2, waits, func(i int, fastDone func()) {
			if i == 0 {
				fastDone()
				close(firstObserving)
				select {
				case <-finish:
				case <-ctx.Done():
				}
			} else {
				close(secondStarted)
				fastDone()
			}
		})
		close(done)
	}()
	select {
	case <-firstObserving:
	case <-ctx.Done():
		t.Fatal("first did not arrive")
	}
	select {
	case <-secondStarted:
	case <-ctx.Done():
		t.Fatal("observation still owns send slot")
	}
	select {
	case <-done:
		t.Fatal("did not wait for observation")
	default:
	}
	close(finish)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("did not finish")
	}
}

func TestDirectDispatchTotalBoundAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := make(chan int, 3)
	for i := 0; i < 3; i++ {
		jobs <- i
	}
	close(jobs)
	waits := make([]directDispatchWait, 3)
	started := make(chan int, 3)
	done := make(chan struct{})
	go func() {
		runDirectBench(ctx, jobs, 1, 2, waits, func(i int, fastDone func()) {
			fastDone()
			started <- i
			<-ctx.Done()
		})
		close(done)
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("not dispatched")
		}
	}
	select {
	case <-started:
		t.Fatal("total limit exceeded")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation leaked a permit")
	}
	if !waits[2].TotalLimited || waits[2].TotalAcquiredUnixNS != 0 {
		t.Fatal("total wait not reported", waits[2])
	}
}

func TestDirectDispatchCancelWhileWaitingForSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := make(chan int, 2)
	jobs <- 0
	jobs <- 1
	close(jobs)
	waits := make([]directDispatchWait, 2)
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runDirectBench(ctx, jobs, 1, 2, waits, func(i int, fastDone func()) {
			if i != 0 {
				t.Error("send limit exceeded")
			}
			close(started)
			<-ctx.Done() // Simulate failing before TXCer persistence, without fastDone.
		})
		close(done)
	}()
	<-started
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("send cancellation stuck")
	}
	if !waits[1].SendLimited || waits[1].TotalAcquiredUnixNS == 0 || waits[1].SendAcquiredUnixNS != 0 {
		t.Fatal(waits[1])
	}
}

func TestDirectDispatchReturnBeforeFastDoneAndLegacyBound(t *testing.T) {
	for _, total := range []int{0, 2} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		jobs := make(chan int, 3)
		for i := 0; i < 3; i++ {
			jobs <- i
		}
		close(jobs)
		waits := make([]directDispatchWait, 3)
		seen := make(chan int, 3)
		runDirectBench(ctx, jobs, 1, total, waits, func(i int, fastDone func()) { seen <- i })
		cancel()
		if len(seen) != 3 {
			t.Fatal("early return leaked send capacity", total)
		}
	}
}
