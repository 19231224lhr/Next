package requesttrace

import (
	"github.com/cometbft/cometbft/libs/operationtrace"
	"testing"
)

func TestCometProfileOptIn(t *testing.T) {
	previous, hook := Consensus, operationtrace.Observe
	defer func() { Consensus = previous; operationtrace.Observe = hook }()
	Consensus = new(ConsensusRecorder)
	operationtrace.Observe = nil
	t.Setenv("UTXO_COMET_PROFILE", "")
	EnableCometProfile()
	operationtrace.Start("test", 9)()
	if len(Consensus.Snapshot()) != 0 {
		t.Fatal("profiling enabled without opt-in")
	}
	t.Setenv("UTXO_COMET_PROFILE", "1")
	EnableCometProfile()
	operationtrace.Start("test", 9)()
	es := Consensus.Snapshot()
	if len(es) != 1 || es[0].Fields["operation"] != "test" || es[0].Fields["height"] != "9" || es[0].Fields["started_ns"] == "" {
		t.Fatal(es)
	}
}
