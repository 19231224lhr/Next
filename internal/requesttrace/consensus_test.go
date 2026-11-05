package requesttrace

import (
	"github.com/cometbft/cometbft/libs/log"
	"testing"
)

func TestConsensusTraceBoundsAndContext(t *testing.T) {
	r := new(ConsensusRecorder)
	l := r.Logger(log.NewNopLogger()).With("height", 7)
	l.Debug("unrelated")
	if len(r.Snapshot()) != 0 {
		t.Fatal("unselected message retained")
	}
	l.With("round", 2).Info("entering prevote step")
	l.Debug("entering commit step")
	e := r.Snapshot()
	if e[0].Fields["height"] != "7" || e[0].Fields["round"] != "2" || e[1].Fields["round"] != "" {
		t.Fatal(e)
	}
	for i := 0; i < 5000; i++ {
		r.Mark("sample", "i", i)
	}
	e = r.Snapshot()
	if len(e) != 4096 || e[0].Fields["i"] != "904" || e[4095].Fields["i"] != "4999" {
		t.Fatal("bad ring order")
	}
	var off *ConsensusRecorder
	off.Mark("disabled")
	if off.Snapshot() != nil {
		t.Fatal("disabled recorder")
	}
}
