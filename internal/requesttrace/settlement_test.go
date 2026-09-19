package requesttrace_test

import (
	"testing"
	"time"
	"utxo/internal/requesttrace"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestSettlementTimingLinksAttemptsAndCommitWithoutOverwriting(t *testing.T) {
	f := testkit.NewFixture("timing", "a", 1)
	cert, err := f.Certify(f.Transaction(0, 1))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := cert.MarshalBinary()
	a := protocol.Submission{Network: f.Org.Network, Body: body}
	raw, _ := a.MarshalBinary()
	r := requesttrace.NewSettlementRecorder(2)
	sent := time.Now().Add(-time.Millisecond).UnixNano()
	received := time.Now().UnixNano()
	r.Receive(raw, sent, received, "gateway")
	r.Command(raw, "accepted", 0)
	r.Command(raw, "mempool_enter", 0)
	r.Command(raw, "mempool_checked", 0)
	r.Command(raw, "execute_start", 7)
	r.Command(raw, "execute_done", 7)
	r.Block(7, "commit_start")
	r.Block(7, "commit_done")
	events := r.ForSpend(protocol.Hash(cert.QC.Fact).String())
	if len(events) != 1 {
		t.Fatal(events)
	}
	e := events[0]
	if e.MempoolEnterUnixNS < received || e.MempoolCheckedUnixNS < e.MempoolEnterUnixNS {
		t.Fatal("mempool timing missing")
	}
	if e.DeliveredUnixNS != sent || e.ReceivedUnixNS != received || e.Height != 7 || e.ExecuteStartUnixNS < received || e.CommittedUnixNS < e.CommitStartUnixNS {
		t.Fatalf("bad trace: %+v", e)
	}
	r.Command(raw, "execute_start", 8)
	if r.ForSpend(e.Spend)[0].Height != 7 {
		t.Fatal("replay overwrote original block")
	}
	for i := byte(1); i < 5; i++ {
		a.Nonce[0] = i
		b, _ := a.MarshalBinary()
		r.Command(b, "accepted", 0)
	}
	if len(r.ForSpend(e.Spend)) != 2 {
		t.Fatal("recorder is not bounded")
	}
	var disabled *requesttrace.SettlementRecorder
	disabled.Receive(raw, sent, received, "wallet")
	disabled.Command(raw, "execute_start", 7)
	disabled.Block(7, "commit_done")
	if len(disabled.ForSpend(e.Spend)) != 0 {
		t.Fatal("disabled tracing collected data")
	}
}
