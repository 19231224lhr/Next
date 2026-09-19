package testkit

import (
	"bytes"
	abci "github.com/cometbft/cometbft/abci/types"
	cmted "github.com/cometbft/cometbft/crypto/ed25519"
	cp "github.com/cometbft/cometbft/proto/tendermint/types"
	cv "github.com/cometbft/cometbft/proto/tendermint/version"
	ct "github.com/cometbft/cometbft/types"
	"time"
	"utxo/finality"
	"utxo/protocol"
)

// Block signs test-only headers with deterministic laboratory keys.
func Block(chain string, height int64, previous []byte, txs [][]byte, results []*abci.ExecTxResult) (finality.Trust, finality.BlockData) {
	keys := map[string]cmted.PrivKey{}
	vals := make([]*ct.Validator, 4)
	for i := range vals {
		seed := protocol.Digest("LAB_BLOCK", []byte(chain), []byte{byte(i)})
		k := cmted.GenPrivKeyFromSecret(seed[:])
		keys[string(k.PubKey().Address())] = k
		vals[i] = ct.NewValidator(k.PubKey(), 1)
	}
	set := ct.NewValidatorSet(vals)
	trust := finality.Trust{ChainID: chain, Network: protocol.Digest("NETWORK", []byte(chain)), Validators: set}
	previousID := ct.BlockID{}
	if height > 1 {
		previousID = ct.BlockID{Hash: previous, PartSetHeader: ct.PartSetHeader{Total: 1, Hash: bytes.Repeat([]byte{1}, 32)}}
	}
	last := &ct.Commit{Height: height - 1, BlockID: previousID}
	if height > 1 {
		last.Signatures = []ct.CommitSig{ct.NewCommitSigAbsent()}
	}
	rawtx := make(ct.Txs, len(txs))
	for i := range txs {
		rawtx[i] = txs[i]
	}
	b := ct.MakeBlock(height, rawtx, last, nil)
	b.Header.Version = cv.Consensus{Block: 11}
	b.ChainID = chain
	b.Time = time.Unix(1700000000+height, 0).UTC()
	b.ValidatorsHash = set.Hash()
	b.NextValidatorsHash = set.Hash()
	b.ProposerAddress = set.Validators[0].Address
	b.LastBlockID = previousID
	h := &ct.Header{Version: cv.Consensus{Block: 11}, ChainID: chain, Height: height + 1, Time: b.Time.Add(time.Second), ValidatorsHash: set.Hash(), NextValidatorsHash: set.Hash(), ProposerAddress: set.Validators[0].Address, LastBlockID: ct.BlockID{Hash: b.Hash(), PartSetHeader: ct.PartSetHeader{Total: 1, Hash: bytes.Repeat([]byte{2}, 32)}}, LastResultsHash: ct.NewResults(results).Hash()}
	id := ct.BlockID{Hash: h.Hash(), PartSetHeader: ct.PartSetHeader{Total: 1, Hash: bytes.Repeat([]byte{3}, 32)}}
	commit := &ct.Commit{Height: h.Height, BlockID: id, Signatures: make([]ct.CommitSig, 4)}
	for i, v := range set.Validators {
		vote := &ct.Vote{Type: cp.PrecommitType, Height: h.Height, BlockID: id, Timestamp: h.Time, ValidatorAddress: v.Address, ValidatorIndex: int32(i)}
		vote.Signature, _ = keys[string(v.Address)].Sign(ct.VoteSignBytes(chain, vote.ToProto()))
		commit.Signatures[i] = vote.CommitSig()
	}
	return trust, finality.BlockData{Block: b, Results: results, Next: ct.SignedHeader{Header: h, Commit: commit}}
}
