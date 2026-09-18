package requesttrace

import (
	"context"
	"sync"
	"testing"
)

func TestOptionalConcurrentRoundTrip(t *testing.T) {
	off := context.Background()
	Mark(off, "ignored")
	if Header(off) != "" {
		t.Fatal("disabled tracing emitted metadata")
	}
	ctx := Start(off, "member")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); Mark(ctx, "step") }()
	}
	wg.Wait()
	recipient := Start(off, "wallet")
	Import(recipient, Header(ctx), "member-0")
	events := Events(recipient)
	if len(events) != 12 {
		t.Fatalf("events=%d", len(events))
	}
	for _, e := range events {
		if e.Node != "member-0" || e.UnixNS <= 0 || e.LocalNS < 0 {
			t.Fatalf("bad event: %+v", e)
		}
	}
	Import(recipient, "invalid", "")
	if len(Events(recipient)) != 12 {
		t.Fatal("invalid diagnostics altered trace")
	}
	events[0].Stage = "mutated"
	if Events(recipient)[0].Stage == "mutated" {
		t.Fatal("snapshot aliases recorder")
	}
	for i := 0; i < 200; i++ {
		Mark(ctx, "bounded")
	}
	if len(Events(ctx)) > 64 {
		t.Fatal("unbounded diagnostics")
	}
}
