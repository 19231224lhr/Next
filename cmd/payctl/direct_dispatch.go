package main

import (
	"context"
	"sync"
	"time"
)

type directDispatchWait struct {
	ReceivedAt                                              time.Time `json:"-"`
	TotalReleasedUnixNS                                     int64
	ReceivedUnixNS, TotalAcquiredUnixNS, SendAcquiredUnixNS int64
	TotalWaitMS, SendWaitMS                                 float64
	TotalLimited, SendLimited                               bool
}

// totalLimit=0 preserves the original complete-lifecycle worker pool.
// Otherwise permits are acquired before spawning, so observers remain bounded.
func runDirectBench(ctx context.Context, jobs <-chan int, sendLimit, totalLimit int, waits []directDispatchWait, run func(int, func())) {
	var wg sync.WaitGroup
	defer wg.Wait()
	if totalLimit == 0 {
		for worker := 0; worker < sendLimit; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					waits[i].ReceivedAt = time.Now()
					waits[i].ReceivedUnixNS = waits[i].ReceivedAt.UnixNano()
					run(i, func() {})
				}
			}()
		}
		return
	}
	total, send := make(chan struct{}, totalLimit), make(chan struct{}, sendLimit)
	acquire := func(permits chan struct{}, waited *float64, limited *bool) bool {
		start := time.Now()
		defer func() { *waited = float64(time.Since(start)) / float64(time.Millisecond) }()
		if ctx.Err() != nil {
			return false
		}
		select {
		case permits <- struct{}{}:
			return true
		default:
			*limited = true
		}
		select {
		case <-ctx.Done():
			return false
		case permits <- struct{}{}:
			// A release and cancellation may become ready together.
			if ctx.Err() != nil {
				<-permits
				return false
			}
			return true
		}
	}
	for i := range jobs {
		w := &waits[i]
		w.ReceivedAt = time.Now()
		w.ReceivedUnixNS = w.ReceivedAt.UnixNano()
		if !acquire(total, &w.TotalWaitMS, &w.TotalLimited) {
			return
		}
		w.TotalAcquiredUnixNS = time.Now().UnixNano()
		if !acquire(send, &w.SendWaitMS, &w.SendLimited) {
			<-total
			w.TotalReleasedUnixNS = time.Now().UnixNano()
			return
		}
		w.SendAcquiredUnixNS = time.Now().UnixNano()
		if ctx.Err() != nil {
			<-send
			<-total
			w.TotalReleasedUnixNS = time.Now().UnixNano()
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-total; w.TotalReleasedUnixNS = time.Now().UnixNano() }()
			// Only this payment's goroutine calls release; no cross-task state.
			released := false
			release := func() {
				if !released {
					<-send
					released = true
				}
			}
			defer release() // Covers HTTP/validation/persistence failure.
			run(i, release)
		}()
	}
}
