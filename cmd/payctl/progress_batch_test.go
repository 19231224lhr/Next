package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
	"utxo/internal/member"
	"utxo/protocol"
)

func TestProgressBatchMatchesFactsAndCoalesces(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := newProgressBatcher(ctx, 256, func(ctx context.Context, fs []protocol.SpendFactID) ([]member.DirectStatus, error) {
		if len(fs) > 128 {
			t.Errorf("oversized %d", len(fs))
		}
		s := make([]member.DirectStatus, len(fs))
		for i, f := range fs {
			s[i] = member.DirectStatus{Observed: true, Height: int64(f[0])}
		}
		return s, nil
	})
	defer func() { cancel(); <-b.done }()
	var wg sync.WaitGroup
	for i := 0; i < 256; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var f protocol.SpendFactID
			f[0] = byte(i)
			s, e := b.Check(ctx, f)
			if e != nil || s.Height != int64(i) || !s.Observed {
				t.Errorf("%d %+v %v", i, s, e)
			}
		}(i)
	}
	wg.Wait()
	if n := b.requests.Load(); n >= 32 || n < 2 {
		t.Fatalf("not coalesced: %d", n)
	}
}
func TestProgressBatchFailureAndCancellation(t *testing.T) {
	for _, bad := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		boom := errors.New("offline")
		b := newProgressBatcher(ctx, 4, func(context.Context, []protocol.SpendFactID) ([]member.DirectStatus, error) {
			if bad {
				return nil, nil
			}
			return nil, boom
		})
		if _, err := b.Check(ctx, protocol.SpendFactID{}); err == nil {
			t.Fatal("failure became success")
		}
		cancel()
		<-b.done
		c, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if _, err := b.Check(c, protocol.SpendFactID{}); err == nil {
			t.Fatal("closed accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	b := newProgressBatcher(ctx, 1, func(ctx context.Context, _ []protocol.SpendFactID) ([]member.DirectStatus, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	returned := make(chan error, 1)
	go func() { _, e := b.Check(ctx, protocol.SpendFactID{}); returned <- e }()
	<-entered
	cancel()
	if e := <-returned; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	<-b.done
}

// Canceling one observer must not cancel the shared HTTP request or lose the
// other observer's exact result, even when the canceled result arrives late.
func TestProgressBatchIndividualCancellation(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	b := newProgressBatcher(ctx, 4, func(ctx context.Context, fs []protocol.SpendFactID) ([]member.DirectStatus, error) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// Give the simulated HTTP operation a measurable duration on platforms
		// whose clock can report zero for an immediately returning mock.
		time.Sleep(time.Millisecond)
		out := make([]member.DirectStatus, len(fs))
		for i, f := range fs {
			out[i].Height = int64(f[0])
		}
		return out, nil
	})
	defer func() { stop(); <-b.done }()
	canceled, cancel := context.WithCancel(ctx)
	first := make(chan error, 1)
	go func() { _, e := b.Check(canceled, protocol.SpendFactID{1}); first <- e }()
	<-entered
	cancel()
	if e := <-first; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	second := make(chan progressResult, 1)
	go func() { v, e := b.Check(ctx, protocol.SpendFactID{2}); second <- progressResult{v, e} }()
	close(release)
	select {
	case got := <-second:
		if got.err != nil || got.status.Height != 2 {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled observer blocked batcher")
	}
	if b.httpNS.Load() == 0 || b.queueNS.Load() == 0 {
		t.Fatalf("missing separate timing: http=%d queue=%d", b.httpNS.Load(), b.queueNS.Load())
	}
}
