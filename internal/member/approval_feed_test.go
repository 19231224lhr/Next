package member_test

import (
	"context"
	"reflect"
	"testing"
	"time"
	"utxo/protocol"
)

func TestApprovedDirectFeedIsReadOnlyAndExpires(t *testing.T) {
	x := newReclaimLab(t)
	deadline := time.Now().Unix() + 60
	tx := expiringApprovalTx(t, x, 0, 1, deadline)
	page, err := x.m[0].ApprovedDirect(nil)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("unapproved transaction leaked", err)
	}
	approval, err := x.m[0].ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx})
	if err != nil {
		t.Fatal(err)
	}
	before, err := x.m[0].Quota(x.f.Genesis.Grants[0].Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	page, err = x.m[0].ApprovedDirect(nil)
	if err != nil || len(page.Items) != 1 {
		t.Fatal("committed approval not available", err)
	}
	got := page.Items[0]
	if got.Request.Tx.ID() != tx.ID() || !reflect.DeepEqual(got.Approval, approval) {
		t.Fatal("feed changed signed material")
	}
	if _, err = x.m[0].ApprovedDirect(page.Next); err != nil {
		t.Fatal(err)
	}
	after, err := x.m[0].Quota(x.f.Genesis.Grants[0].Key, 0)
	if err != nil || before != after {
		t.Fatal("read reserved quota again", err)
	}
	expiryBlock(t, x, 1, nil, deadline)
	page, err = x.m[0].ApprovedDirect(nil)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("expired approval still advertised", err)
	}
}
