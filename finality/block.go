package finality

import (
	"bytes"
	"fmt"
	abci "github.com/cometbft/cometbft/abci/types"
	ct "github.com/cometbft/cometbft/types"
)

// BlockData contains ordinary ledger data, shared by every reader of a height.
type BlockData struct {
	Block   *ct.Block
	Results []*abci.ExecTxResult
	Next    ct.SignedHeader
}
type ExecutedTx struct {
	Bytes []byte
	Code  uint32
	Data  []byte
}
type VerifiedBlock struct {
	height         int64
	hash, previous []byte
	txs            []ExecutedTx
}

func (b VerifiedBlock) Height() int64    { return b.height }
func (b VerifiedBlock) Hash() []byte     { return bytes.Clone(b.hash) }
func (b VerifiedBlock) Previous() []byte { return bytes.Clone(b.previous) }
func (b VerifiedBlock) Transactions() []ExecutedTx {
	out := make([]ExecutedTx, len(b.txs))
	for i, t := range b.txs {
		out[i] = ExecutedTx{bytes.Clone(t.Bytes), t.Code, bytes.Clone(t.Data)}
	}
	return out
}
func VerifyBlock(t Trust, p BlockData) (VerifiedBlock, error) {
	fail := VerifiedBlock{}
	b, h := p.Block, p.Next
	if t.Validate() != nil || b == nil || h.Header == nil || h.Commit == nil || b.ChainID != t.ChainID || h.Height != b.Height+1 || b.Height < 1 || len(b.Txs) != len(p.Results) {
		return fail, ErrProof
	}
	if err := b.ValidateBasic(); err != nil {
		return fail, fmt.Errorf("block %d: %w", b.Height, err)
	}
	if err := h.ValidateBasic(t.ChainID); err != nil {
		return fail, fmt.Errorf("next header: %w", err)
	}
	if !bytes.Equal(h.ValidatorsHash, t.Validators.Hash()) || !bytes.Equal(h.NextValidatorsHash, t.Validators.Hash()) || !bytes.Equal(h.LastBlockID.Hash, b.Hash()) {
		return fail, fmt.Errorf("block/header linkage: %w", ErrProof)
	}
	// Compute from supplied bytes, not cached fields in the decoded block.
	data := ct.Data{Txs: b.Txs}
	if !bytes.Equal(data.Hash(), b.DataHash) {
		return fail, fmt.Errorf("transaction root: %w", ErrProof)
	}
	for _, r := range p.Results {
		if r == nil {
			return fail, ErrProof
		}
	}
	if !bytes.Equal(ct.NewResults(p.Results).Hash(), h.LastResultsHash) {
		return fail, fmt.Errorf("execution root at %d: %w", b.Height, ErrProof)
	}
	if err := t.Validators.VerifyCommitLight(t.ChainID, h.Commit.BlockID, h.Height, h.Commit); err != nil {
		return fail, err
	}
	out := VerifiedBlock{height: b.Height, hash: bytes.Clone(b.Hash()), previous: bytes.Clone(b.LastBlockID.Hash)}
	for i, tx := range b.Txs {
		r := p.Results[i]
		out.txs = append(out.txs, ExecutedTx{bytes.Clone(tx), r.Code, bytes.Clone(r.Data)})
	}
	return out, nil
}
