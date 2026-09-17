package testkit

import (
	"bytes"
	cmted "github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/cometbft/cometbft/crypto/merkle"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	version "github.com/cometbft/cometbft/proto/tendermint/version"
	ct "github.com/cometbft/cometbft/types"
	"time"
	"utxo/finality"
	"utxo/protocol"
)

// Proof signs a synthetic, explicit test committee header. Production code must
// obtain the header from running consensus instead.
func Proof(chain string, fact protocol.FinalFact) (finality.Trust, finality.FactProof, error) {
	keys := make(map[string]cmted.PrivKey)
	vals := make([]*ct.Validator, 4)
	for i := range vals {
		seed := protocol.Digest("LAB_COMMITTEE", []byte(chain), []byte{byte(i)})
		key := cmted.GenPrivKeyFromSecret(seed[:])
		keys[string(key.PubKey().Address())] = key
		vals[i] = ct.NewValidator(key.PubKey(), 1)
	}
	set := ct.NewValidatorSet(vals)
	trust := finality.Trust{ChainID: chain, Network: protocol.Digest("NETWORK", []byte(chain)), Validators: set}
	leaf, e := fact.MarshalBinary()
	if e != nil {
		return trust, finality.FactProof{}, e
	}
	root, paths := merkle.ProofsFromByteSlices([][]byte{leaf})
	commitment := finality.Commitment{Network: trust.Network, Height: 1, WriteSet: protocol.Digest("LAB_WRITES", nil), FactRoot: root}
	hash, e := commitment.Hash()
	if e != nil {
		return trust, finality.FactProof{}, e
	}
	header := &ct.Header{Version: version.Consensus{Block: 11}, ChainID: chain, Height: 2, Time: time.Unix(1700000000, 0).UTC(), ValidatorsHash: set.Hash(), NextValidatorsHash: set.Hash(), AppHash: hash[:], ProposerAddress: set.Validators[0].Address}
	block := ct.BlockID{Hash: header.Hash(), PartSetHeader: ct.PartSetHeader{Total: 1, Hash: bytes.Repeat([]byte{4}, 32)}}
	commit := &ct.Commit{Height: 2, BlockID: block, Signatures: make([]ct.CommitSig, 4)}
	for i, v := range set.Validators {
		vote := &ct.Vote{Type: cmtproto.PrecommitType, Height: 2, BlockID: block, Timestamp: header.Time, ValidatorAddress: v.Address, ValidatorIndex: int32(i)}
		vote.Signature, e = keys[string(v.Address)].Sign(ct.VoteSignBytes(chain, vote.ToProto()))
		if e != nil {
			return trust, finality.FactProof{}, e
		}
		commit.Signatures[i] = vote.CommitSig()
	}
	return trust, finality.FactProof{Fact: fact, Commitment: commitment, Path: *paths[0], Header: ct.SignedHeader{Header: header, Commit: commit}}, nil
}
