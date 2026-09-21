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

// Admission is serialized immediately before HTTP send, after acquiring permits.
// Retain at most four sends of credit; a stalled observer cannot build a large burst.
// Original scheduled timestamps remain unchanged in benchDirect.
func newDirectPacer(perSecond float64) func(context.Context) error {
	if perSecond == 0 {
		return func(ctx context.Context) error { return ctx.Err() }
	}
	interval := time.Duration(float64(time.Second) / perSecond)
	gate := make(chan struct{}, 1)
	var next time.Time
	return func(ctx context.Context) error {
		select {
		case gate <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		defer func() { <-gate }()
		if err := ctx.Err(); err != nil {
			return err
		}
		floor := time.Now().Add(-3 * interval)
		if next.Before(floor) {
			next = floor
		}
		if delay := time.Until(next); delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		next = next.Add(interval)
		return nil
	}
}
