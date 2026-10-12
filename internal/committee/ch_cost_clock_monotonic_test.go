//go:build comet_v3 && !windows

package committee

import (
	"testing"
	"time"
)

func chCostClock(t *testing.T) (func() int64, int64, string) {
	origin := time.Now()
	return func() int64 { return time.Since(origin).Nanoseconds() }, 1000000000, "Go monotonic time.Since from fixed time.Now origin"
}
