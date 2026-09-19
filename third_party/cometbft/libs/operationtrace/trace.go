// Package operationtrace is an optional, process-local diagnostic hook.
// Configure Observe once before starting nodes. It never changes ledger data.
package operationtrace

import "time"

var Observe func(stage string, height int64, started time.Time, elapsed time.Duration)

func Start(stage string, height int64) func() {
	observe := Observe
	if observe == nil {
		return func() {}
	}
	started := time.Now()
	return func() { observe(stage, height, started, time.Since(started)) }
}
