package protocol

import "testing"

func TestCompactApprovalReconstructsAndBounds(t *testing.T) {
	tx := FastTx{Body: TxBody{Network: Digest("net"), Outputs: []Output{{Asset: AssetCAL, Amount: 17}}}}
	v := AdmissionVector{{Key: ResourceKey{Kind: ResourceCAL, Account: Digest("org"), Version: 1}, Cap: 17}}
	a := DirectApproval{Summary: SummaryFor(tx, v), Vote: SpendVote{Member: 2}}
	raw, e := a.MarshalCompact()
	if e != nil {
		t.Fatal(e)
	}
	got, e := DecodeCompactDirectApproval(raw, tx)
	if e != nil {
		t.Fatal(e)
	}
	if got.Summary.Fact() != a.Summary.Fact() || got.Vote != a.Vote {
		t.Fatal("changed signed fact")
	}
	for _, bad := range [][]byte{raw[:len(raw)-1], append(append([]byte{}, raw...), 0), make([]byte, MaxCompactApprovalBytes+1)} {
		if _, e := DecodeCompactDirectApproval(bad, tx); e == nil {
			t.Fatal("malformed response accepted")
		}
	}
}
