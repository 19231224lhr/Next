package finality_test

import (
	"bytes"
	"crypto/sha256"
	abci "github.com/cometbft/cometbft/abci/types"
	ct "github.com/cometbft/cometbft/types"
	"testing"
	"utxo/finality"
	"utxo/internal/testkit"
)

func TestReaderHashNeedsNoRepairKey(t *testing.T) {
	a := ct.EncodeRedactableTx([]byte("fixed"), []byte{1, 2})
	b := ct.EncodeRedactableTx([]byte("fixed"), []byte{3, 4})
	expected := sha256.Sum256(a[:len(a)-2])
	if !bytes.Equal(a.Hash(), expected[:]) || !bytes.Equal(a.Hash(), b.Hash()) {
		t.Fatal("reader hashed mutable funding bytes")
	}
	b[16] ^= 1
	if bytes.Equal(a.Hash(), b.Hash()) {
		t.Fatal("fixed transaction body not bound")
	}
}

// A next-header proof authenticates the fixed transaction projection and result,
// not the current mutable history representation. History readers need a
// separate revision proof; economic followers must not use mutable funding.
func TestBlockProofAuthenticatesProjectionNotMutableHistory(t *testing.T) {
	original := ct.EncodeRedactableTx([]byte("fixed"), []byte{1, 2})
	trust, data := testkit.Block("projection", 1, nil, [][]byte{original}, []*abci.ExecTxResult{{Data: []byte{3}}})
	before, err := finality.VerifyBlock(trust, data)
	if err != nil {
		t.Fatal(err)
	}
	data.Block.Txs[0] = ct.EncodeRedactableTx([]byte("fixed"), []byte{3, 4})
	after, err := finality.VerifyBlock(trust, data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Hash(), after.Hash()) || !bytes.Equal(before.Transactions()[0].Data, after.Transactions()[0].Data) {
		t.Fatal("mutable representation changed authenticated economic projection")
	}
	if bytes.Equal(before.Transactions()[0].Bytes, after.Transactions()[0].Bytes) {
		t.Fatal("fixture did not change the supplied history representation")
	}
	data.Results[0].Data[0]++
	if _, err := finality.VerifyBlock(trust, data); err == nil {
		t.Fatal("execution result was not authenticated")
	}
}
