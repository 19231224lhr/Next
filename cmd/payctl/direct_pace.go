package main

import (
	"context"
	"time"
)

// A bounded worker pool provides backpressure. Scheduled and actual send times
// are both reported: falling behind the target cannot masquerade as low latency.
func directBenchJobs(ctx context.Context, count int, rate float64, start time.Time) <-chan int {
	jobs := make(chan int)
	go func() {
		defer close(jobs)
		timer := time.NewTimer(0)
		defer timer.Stop()
		for i := 0; i < count; i++ {
			if ctx.Err() != nil {
				return
			}
			if rate > 0 {
				due := start.Add(time.Duration(float64(i) / rate * float64(time.Second)))
				timer.Reset(max(time.Until(due), 0))
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
			}
			select {
			case <-ctx.Done():
				return
			case jobs <- i:
			}
		}
	}()
	return jobs
}
