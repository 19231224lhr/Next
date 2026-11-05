package member_test

import (
	"context"
	"testing"
	"time"
	"utxo/protocol"
)

func TestCALDemandCountsApprovalOnceAndReportsBlockedSize(t *testing.T) {
	x := newReclaimLab(t)
	tx := expiringApprovalTx(t, x, 0, 1, time.Now().Unix()+60)
	req := protocol.DirectRequest{Tx: tx}
	if _, e := x.m[0].ApproveDirect(context.Background(), req); e != nil {
		t.Fatal(e)
	}
	if _, e := x.m[0].ApproveDirect(context.Background(), req); e != nil {
		t.Fatal(e)
	}
	amount, largest := x.m[0].CALDemand()
	if amount != 100 || largest != 100 {
		t.Fatal("replay inflated demand", amount, largest)
	}
	blocked := expiringApprovalTx(t, x, 1, 2, time.Now().Unix()+60)
	if _, e := x.m[0].ApproveDirect(context.Background(), protocol.DirectRequest{Tx: blocked}); e == nil {
		t.Fatal("expected exhausted quota")
	}
	amount, largest = x.m[0].CALDemand()
	if amount != 100 || largest != 100 {
		t.Fatal("failed request counted as approved", amount, largest)
	}
}
