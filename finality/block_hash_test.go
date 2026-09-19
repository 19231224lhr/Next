package finality_test

import (
	"bytes"
	"crypto/sha256"
	ct "github.com/cometbft/cometbft/types"
	"testing"
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
