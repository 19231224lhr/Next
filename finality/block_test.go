package finality_test

import (
	abci "github.com/cometbft/cometbft/abci/types"
	ct "github.com/cometbft/cometbft/types"
	"testing"
	"utxo/finality"
	"utxo/internal/testkit"
)

func TestBlockAuthenticatesExecutionAndFreezesBytes(t *testing.T) {
	fixture := func() (finality.Trust, finality.BlockData) {
		return testkit.Block("block", 1, nil, [][]byte{{1, 2}}, []*abci.ExecTxResult{{Code: 2, Data: []byte{3}}})
	}
	trust, p := fixture()
	b, err := finality.VerifyBlock(trust, p)
	if err != nil {
		t.Fatal(err)
	}
	if b.Transactions()[0].Code != 2 {
		t.Fatal("failed execution lost")
	}
	p.Block.Txs[0][0] = 9
	p.Results[0].Data[0] = 9
	if b.Transactions()[0].Bytes[0] != 1 || b.Transactions()[0].Data[0] != 3 {
		t.Fatal("aliased untrusted bytes")
	}
	for _, change := range []func(*finality.BlockData){func(p *finality.BlockData) { p.Results[0].Code = 0 }, func(p *finality.BlockData) { p.Results[0].Data[0]++ }, func(p *finality.BlockData) { p.Block.Txs[0][0]++ }, func(p *finality.BlockData) {
		p.Next.Commit.Signatures[0] = ct.NewCommitSigAbsent()
		p.Next.Commit.Signatures[1] = ct.NewCommitSigAbsent()
	}} {
		trust, p = fixture()
		change(&p)
		if _, err = finality.VerifyBlock(trust, p); err == nil {
			t.Fatal("accepted altered block/result or insufficient quorum")
		}
	}
}
