package requesttrace

import (
	"os"
	"time"

	"github.com/cometbft/cometbft/libs/operationtrace"
)

// EnableCometProfile is called before starting the node. Timings are diagnostic
// only, kept in the existing bounded recorder; no synchronous log is added.
func EnableCometProfile() {
	if Consensus == nil || os.Getenv("UTXO_COMET_PROFILE") != "1" {
		return
	}
	operationtrace.Observe = func(stage string, height int64, started time.Time, elapsed time.Duration) {
		Consensus.Mark("comet_operation", "operation", stage, "height", height, "started_ns", started.UnixNano(), "elapsed_ns", elapsed.Nanoseconds())
	}
}
