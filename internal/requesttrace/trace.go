// Package requesttrace provides opt-in, bounded diagnostic metadata, never protocol evidence.
package requesttrace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"runtime/trace"
	"sort"
	"strconv"
	"sync"
	"time"
)

const HeaderName = "X-UTXO-Diagnostic-Trace"
const maxEvents = 128
const maxHeader = 24576

type Event struct {
	Node    string `json:"node"`
	Stage   string `json:"stage"`
	UnixNS  int64  `json:"unix_ns"`
	LocalNS int64  `json:"local_ns"`
}
type key struct{}
type recorder struct {
	mu     sync.Mutex
	node   string
	start  time.Time
	events []Event
}

func Start(ctx context.Context, node string) context.Context {
	return context.WithValue(ctx, key{}, &recorder{node: node, start: time.Now()})
}
func Enabled(ctx context.Context) bool { return ctx.Value(key{}) != nil }
func Mark(ctx context.Context, stage string) {
	MarkNode(ctx, "", stage)
}

// MarkNode labels concurrent client-side spans without changing the recorder.
func MarkNode(ctx context.Context, node, stage string) {
	r, _ := ctx.Value(key{}).(*recorder)
	if r == nil {
		return
	}
	now := time.Now()
	if trace.IsEnabled() {
		trace.Log(ctx, "utxo", strconv.FormatInt(now.UnixNano(), 10)+":"+stage)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) < maxEvents {
		if node == "" {
			node = r.node
		}
		r.events = append(r.events, Event{node, stage, now.UnixNano(), now.Sub(r.start).Nanoseconds()})
	}
}
func Events(ctx context.Context) []Event {
	r, _ := ctx.Value(key{}).(*recorder)
	if r == nil {
		return nil
	}
	r.mu.Lock()
	events := append([]Event(nil), r.events...)
	r.mu.Unlock()
	sort.SliceStable(events, func(i, j int) bool { return events[i].UnixNS < events[j].UnixNS })
	return events
}
func Header(ctx context.Context) string {
	if !Enabled(ctx) {
		return ""
	}
	raw, _ := json.Marshal(Events(ctx))
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	if len(encoded) > maxHeader {
		return ""
	}
	return encoded
}

// Import ignores malformed or oversized metadata. Diagnostics never control payment acceptance.
func Import(ctx context.Context, encoded, source string) {
	r, _ := ctx.Value(key{}).(*recorder)
	if r == nil || len(encoded) > maxHeader {
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return
	}
	var events []Event
	if json.Unmarshal(raw, &events) != nil || len(events) > maxEvents {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range events {
		if len(r.events) >= maxEvents {
			break
		}
		if source != "" {
			e.Node = source
		}
		r.events = append(r.events, e)
	}
}
