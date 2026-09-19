package requesttrace_test

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
	"utxo/internal/requesttrace"
	"utxo/internal/rules"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestSettlementRecordsV4DirectCommand(t *testing.T) {
	f := testkit.NewFixture("trace-v4", "org", 1)
	f.EnableDirect()
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	settings := rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	policy, err := settings.Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.FastTransaction(0, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := f.DirectCertificate(tx, policy)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = (protocol.DirectPayment{Tx: tx, Certificate: cert}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	r := requesttrace.NewSettlementRecorder(4)
	r.Receive(raw, 1, 2, "gateway")
	for _, stage := range []string{"accepted", "prepared", "proposal_seen", "final_check_start", "final_check_done", "execute_start", "execute_done"} {
		r.Command(raw, stage, 7)
	}
	r.Block(7, "commit_done")
	fact := protocol.Hash(cert.QC.Fact).String()
	got := r.ForSpend(fact)
	if len(got) != 1 {
		t.Fatalf("v4 trace missing: %+v", got)
	}
	e := got[0]
	if e.Height != 7 || e.FinalCheckStartUnixNS == 0 || e.FinalCheckDoneUnixNS < e.FinalCheckStartUnixNS || e.ExecuteStartUnixNS < e.FinalCheckDoneUnixNS || e.CommittedUnixNS < e.ExecuteDoneUnixNS {
		t.Fatalf("incorrect boundaries: %+v", e)
	}
	r.Command(raw, "final_check_start", 8)
	r.Command(raw, "execute_start", 8)
	if again := r.ForSpend(fact)[0]; again != e {
		t.Fatal("retry overwrote original timing")
	}
}
