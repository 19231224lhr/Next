package finality

import (
	"bytes"
	cmted "github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/cometbft/cometbft/crypto/merkle"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmtversion "github.com/cometbft/cometbft/proto/tendermint/version"
	cmttypes "github.com/cometbft/cometbft/types"
	"testing"
	"time"
	"utxo/protocol"
)

func proofFixture(t *testing.T) (Trust, FactProof) {
	t.Helper()
	network := protocol.Digest("NETWORK", []byte("lab"))
	keys := make(map[string]cmted.PrivKey)
	vals := make([]*cmttypes.Validator, 4)
	for i := range vals {
		k := cmted.GenPrivKey()
		keys[string(k.PubKey().Address())] = k
		vals[i] = cmttypes.NewValidator(k.PubKey(), 1)
	}
	set := cmttypes.NewValidatorSet(vals)
	trust := Trust{ChainID: "lab", Network: network, Validators: set}
	fact := protocol.FinalFact{Kind: protocol.FactGrant, Key: protocol.Digest("grant", nil), Revision: 1, Network: network, Payload: []byte{1, 2, 3}}
	leaf, _ := fact.MarshalBinary()
	root, paths := merkle.ProofsFromByteSlices([][]byte{leaf})
	commitment := Commitment{Network: network, Height: 1, WriteSet: protocol.Digest("writes", nil), FactRoot: root}
	hash, e := commitment.Hash()
	if e != nil {
		t.Fatal(e)
	}
	header := &cmttypes.Header{Version: cmtversion.Consensus{Block: 11}, ChainID: "lab", Height: 2, Time: time.Unix(1700000000, 0).UTC(), ValidatorsHash: set.Hash(), NextValidatorsHash: set.Hash(), AppHash: hash[:], ProposerAddress: set.Validators[0].Address}
	block := cmttypes.BlockID{Hash: header.Hash(), PartSetHeader: cmttypes.PartSetHeader{Total: 1, Hash: bytes.Repeat([]byte{4}, 32)}}
	commit := &cmttypes.Commit{Height: 2, Round: 0, BlockID: block, Signatures: make([]cmttypes.CommitSig, 4)}
	for i, v := range set.Validators {
		vote := &cmttypes.Vote{Type: cmtproto.PrecommitType, Height: 2, Round: 0, BlockID: block, Timestamp: header.Time, ValidatorAddress: v.Address, ValidatorIndex: int32(i)}
		signature, e := keys[string(v.Address)].Sign(cmttypes.VoteSignBytes("lab", vote.ToProto()))
		if e != nil {
			t.Fatal(e)
		}
		vote.Signature = signature
		commit.Signatures[i] = vote.CommitSig()
	}
	return trust, FactProof{Fact: fact, Commitment: commitment, Path: *paths[0], Header: cmttypes.SignedHeader{Header: header, Commit: commit}}
}
func TestP03FactNeedsNextAuthenticatedHeader(t *testing.T) {
	trust, p := proofFixture(t)
	v, e := Verify(trust, p)
	if e != nil {
		t.Fatal(e)
	}
	if v.Fact().ID() != p.Fact.ID() {
		t.Fatal("wrong fact")
	}
	p.Fact.Payload[0] ^= 1
	if _, e = Verify(trust, p); e == nil {
		t.Fatal("altered fact")
	}
	if v.Fact().Payload[0] != 1 {
		t.Fatal("verified fact aliases untrusted proof")
	}
	trust, p = proofFixture(t)
	p.Commitment.Height = 2
	if _, e = Verify(trust, p); e == nil {
		t.Fatal("same-height proof")
	}
	trust, p = proofFixture(t)
	p.Header.Commit.Signatures[0] = cmttypes.NewCommitSigAbsent()
	p.Header.Commit.Signatures[1] = cmttypes.NewCommitSigAbsent()
	if _, e = Verify(trust, p); e == nil {
		t.Fatal("two votes")
	}
	trust, p = proofFixture(t)
	other, _ := proofFixture(t)
	trust.Validators = other.Validators
	if _, e = Verify(trust, p); e == nil {
		t.Fatal("foreign committee")
	}
}
func TestProofRoundTrip(t *testing.T) {
	trust, p := proofFixture(t)
	b, e := p.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	q, e := Decode(b)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Verify(trust, q); e != nil {
		t.Fatal(e)
	}
	if _, e = Decode(append(b, 0)); e == nil {
		t.Fatal("trailing bytes")
	}
}
