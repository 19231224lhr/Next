package finality

import (
	"bytes"
	"errors"
	"github.com/cometbft/cometbft/crypto/merkle"
	cryptoproto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"utxo/protocol"
)

var ErrProof = errors.New("invalid final fact proof")

type Commitment struct {
	Network  protocol.Hash
	Height   int64
	Previous []byte
	WriteSet protocol.Hash
	FactRoot []byte
}

func (c Commitment) Hash() (protocol.Hash, error) {
	if c.Height <= 0 || c.Network == (protocol.Hash{}) || len(c.Previous) != 0 && len(c.Previous) != 32 || len(c.FactRoot) != 32 {
		return protocol.Hash{}, ErrProof
	}
	e := new(protocol.Encoder)
	e.U64(uint64(c.Height))
	return protocol.Digest("APP_V2", c.Network[:], e.Data(), c.Previous, c.WriteSet[:], c.FactRoot), nil
}

type Trust struct {
	ChainID    string
	Network    protocol.Hash
	Validators *cmttypes.ValidatorSet
}

func (t Trust) Validate() error {
	if t.ChainID == "" || t.Network == (protocol.Hash{}) || t.Validators == nil || len(t.Validators.Validators) != 4 {
		return ErrProof
	}
	weight := t.Validators.Validators[0].VotingPower
	if weight <= 0 {
		return ErrProof
	}
	seen := make(map[string]bool)
	for _, v := range t.Validators.Validators {
		if v.ValidateBasic() != nil || v.VotingPower != weight || seen[string(v.Address)] {
			return ErrProof
		}
		seen[string(v.Address)] = true
	}
	return nil
}

type FactProof struct {
	Fact       protocol.FinalFact
	Commitment Commitment
	Path       merkle.Proof
	Header     cmttypes.SignedHeader
}
type VerifiedFact struct{ fact protocol.FinalFact }

func (v VerifiedFact) Fact() protocol.FinalFact {
	f := v.fact
	f.Payload = bytes.Clone(f.Payload)
	return f
}
func Verify(t Trust, p FactProof) (VerifiedFact, error) {
	fail := VerifiedFact{}
	if t.Validate() != nil || p.Fact.Network != t.Network || p.Commitment.Network != t.Network || p.Header.Header == nil || p.Header.Commit == nil {
		return fail, ErrProof
	}
	if p.Header.Height != p.Commitment.Height+1 || p.Header.ChainID != t.ChainID || !bytes.Equal(p.Header.ValidatorsHash, t.Validators.Hash()) || !bytes.Equal(p.Header.NextValidatorsHash, t.Validators.Hash()) {
		return fail, ErrProof
	}
	root, e := p.Commitment.Hash()
	if e != nil || !bytes.Equal(root[:], p.Header.AppHash) {
		return fail, ErrProof
	}
	if e = p.Header.ValidateBasic(t.ChainID); e != nil {
		return fail, errors.Join(ErrProof, e)
	}
	if e = t.Validators.VerifyCommitLight(t.ChainID, p.Header.Commit.BlockID, p.Header.Height, p.Header.Commit); e != nil {
		return fail, errors.Join(ErrProof, e)
	}
	leaf, e := p.Fact.MarshalBinary()
	if e != nil {
		return fail, e
	}
	if e = p.Path.Verify(p.Commitment.FactRoot, leaf); e != nil {
		return fail, errors.Join(ErrProof, e)
	}
	f := p.Fact
	f.Payload = bytes.Clone(f.Payload)
	return VerifiedFact{fact: f}, nil
}

const MaxProofBytes = protocol.MaxFactBytes + 128*1024

func (p FactProof) MarshalBinary() ([]byte, error) {
	fact, e := p.Fact.MarshalBinary()
	if e != nil {
		return nil, e
	}
	if _, e = p.Commitment.Hash(); e != nil {
		return nil, e
	}
	path, e := p.Path.ToProto().Marshal()
	if e != nil {
		return nil, e
	}
	if p.Header.Header == nil || p.Header.Commit == nil {
		return nil, ErrProof
	}
	header, e := p.Header.ToProto().Marshal()
	if e != nil {
		return nil, e
	}
	enc := new(protocol.Encoder)
	enc.U16(60)
	enc.U64(protocol.WireVersion)
	enc.Bytes(fact)
	enc.Fixed(p.Commitment.Network[:])
	enc.U64(uint64(p.Commitment.Height))
	enc.Bytes(p.Commitment.Previous)
	enc.Fixed(p.Commitment.WriteSet[:])
	enc.Bytes(p.Commitment.FactRoot)
	enc.Bytes(path)
	enc.Bytes(header)
	if len(enc.Data()) > MaxProofBytes {
		return nil, ErrProof
	}
	return enc.Data(), nil
}
func Decode(b []byte) (p FactProof, err error) {
	if len(b) > MaxProofBytes {
		return p, protocol.ErrEncoding
	}
	d := protocol.NewDecoder(b)
	if d.U16() != 60 || d.U64() != protocol.WireVersion {
		return p, protocol.ErrEncoding
	}
	p.Fact, err = protocol.DecodeFact(d.Bytes(protocol.MaxFactBytes + 256))
	if err != nil {
		return p, err
	}
	copy(p.Commitment.Network[:], d.Fixed(32))
	p.Commitment.Height = int64(d.U64())
	p.Commitment.Previous = bytes.Clone(d.Bytes(32))
	copy(p.Commitment.WriteSet[:], d.Fixed(32))
	p.Commitment.FactRoot = bytes.Clone(d.Bytes(32))
	path := new(cryptoproto.Proof)
	if err = path.Unmarshal(d.Bytes(4096)); err != nil {
		return p, err
	}
	mp, e := merkle.ProofFromProto(path)
	if e != nil {
		return p, e
	}
	p.Path = *mp
	header := new(cmtproto.SignedHeader)
	if err = header.Unmarshal(d.Bytes(64 * 1024)); err != nil {
		return p, err
	}
	sh, e := cmttypes.SignedHeaderFromProto(header)
	if e != nil {
		return p, e
	}
	p.Header = *sh
	if err = d.Done(); err != nil {
		return p, err
	}
	_, err = p.Commitment.Hash()
	return
}
