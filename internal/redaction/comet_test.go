//go:build comet_v3

package redaction_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	tss "github.com/cloudflare/circl/tss/rsa"
	dbm "github.com/cometbft/cometbft-db"
	"github.com/cometbft/cometbft/consensus"
	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtcons "github.com/cometbft/cometbft/proto/tendermint/consensus"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
	"utxo/crypto/chameleon"
)

const chain = "utxo-v3-redaction-gate"

// Isolates the extra admission check; this is not a consensus TPS benchmark.
func BenchmarkOriginalPartGate(b *testing.B) {
	var opening chameleon.Opening
	opening[chameleon.Size-1] = 1
	part := &types.Part{Redaction: &types.PartRedaction{Height: 1, Opening: opening}}
	cases := []struct {
		name string
		gate func(*types.Part, int64) error
	}{
		{"legacy_label_only", func(p *types.Part, height int64) error {
			if p != nil && p.Redaction != nil && (p.Redaction.Height != height || p.Redaction.Revision != 0) {
				return types.ErrRedaction
			}
			return nil
		}},
		{"canonical_opening", (*types.Part).ValidateOriginal},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := tc.gate(part, 1); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The revision label is not part of the commitment: an old valid adaptation
// must not enter live consensus merely by relabelling it as revision zero.
func TestConsensusRejectsRelabelledOpening(t *testing.T) {
	p, signers, vals, _ := committee(t)
	oldTx := types.EncodeRedactableTx([]byte("fixed"), []byte("old"))
	b := block(t, 1, oldTx, &types.Commit{}, vals)
	initial, err := b.MakePartSet(types.BlockPartSizeBytes)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Total() != 1 {
		t.Fatal("fixture must fit one part")
	}
	pb, err := b.ToProto()
	if err != nil {
		t.Fatal(err)
	}
	pb.Data.Txs[0] = types.EncodeRedactableTx([]byte("fixed"), []byte("new"))
	body, err := pb.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	old := initial.GetPart(0)
	ctx := types.RedactionContext(1, 0)
	c, err := p.Digest(ctx, old.Bytes, old.Redaction.Opening)
	if err != nil {
		t.Fatal(err)
	}
	r := adapt(t, p, signers, ctx, old.Bytes, body, c, old.Redaction.Opening)
	revised, err := types.NewRedactablePartSet(body, types.BlockPartSizeBytes, 1, 1, []chameleon.Opening{r})
	if err != nil {
		t.Fatal(err)
	}
	if !revised.Header().Equals(initial.Header()) {
		t.Fatal("fixture must retain the signed root")
	}
	part := revised.GetPart(0)
	part.Redaction.Revision = 0 // Attacker-controlled metadata, no private key needed.
	wire, err := part.ToProto()
	if err != nil {
		t.Fatal(err)
	}
	part, err = types.PartFromProto(wire)
	if err != nil {
		t.Fatal(err)
	}
	// Generic inclusion still succeeds; consensus needs an additional rule.
	received := types.NewPartSetFromHeader(initial.Header())
	if _, err := received.AddPart(part); err != nil {
		t.Fatal(err)
	}
	if err := (&consensus.BlockPartMessage{Height: 1, Part: part}).ValidateBasic(); err == nil {
		t.Fatal("live consensus accepted relabelled adapted opening")
	}
	if _, err := consensus.MsgFromProto(&cmtcons.BlockPart{Height: 1, Part: *wire}); err == nil {
		t.Fatal("network message decoder accepted relabelled adapted opening")
	}
}

func TestConsensusRejectsUncertifiedPartRevision(t *testing.T) {
	_, _, vals, _ := committee(t)
	b := block(t, 1, types.Tx("ordinary"), &types.Commit{}, vals)
	parts, err := b.MakePartSet(types.BlockPartSizeBytes)
	if err != nil {
		t.Fatal(err)
	}
	part := parts.GetPart(0)
	msg := &consensus.BlockPartMessage{Height: 1, Part: part}
	if err = msg.ValidateBasic(); err != nil {
		t.Fatal(err)
	}
	part.Redaction.Revision = 1
	if err = msg.ValidateBasic(); err == nil {
		t.Fatal("live consensus accepted unauthenticated revision label")
	}
	part.Redaction.Revision = 0
	part.Redaction.Height = 2
	if err = msg.ValidateBasic(); err == nil {
		t.Fatal("part context height differs from consensus height")
	}
	part.Redaction.Height = 1
	part.Redaction.Opening[0] = 1
	if err = msg.ValidateBasic(); err == nil {
		t.Fatal("noncanonical original opening accepted")
	}
	part.Redaction = nil
	if err = msg.ValidateBasic(); err == nil {
		t.Fatal("missing redaction metadata accepted")
	}
	msg.Part = nil
	if err = msg.ValidateBasic(); err == nil {
		t.Fatal("missing part accepted")
	}
}

func committee(t *testing.T) (*chameleon.Public, []chameleon.Signer, *types.ValidatorSet, map[string]ed25519.PrivKey) {
	t.Helper()
	b, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := pem.Decode(b)
	k, err := x509.ParsePKCS1PrivateKey(p.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := chameleon.NewPublic(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	shares, err := tss.Deal(rand.Reader, 4, 3, k, true)
	if err != nil {
		t.Fatal(err)
	}
	signers := make([]chameleon.Signer, 4)
	for i := range shares {
		signers[i], err = chameleon.NewSigner(pub, shares[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := types.ConfigureRedaction(chain, pub); err != nil {
		t.Fatal(err)
	}
	keys := map[string]ed25519.PrivKey{}
	var vals []*types.Validator
	for i := 0; i < 4; i++ {
		key := ed25519.GenPrivKey()
		keys[string(key.PubKey().Address())] = key
		vals = append(vals, types.NewValidator(key.PubKey(), 10))
	}
	return pub, signers, types.NewValidatorSet(vals), keys
}

func commit(t *testing.T, height int64, id types.BlockID, vals *types.ValidatorSet, keys map[string]ed25519.PrivKey) *types.Commit {
	t.Helper()
	sigs := make([]types.CommitSig, 4)
	for i, val := range vals.Validators {
		if i == 3 {
			sigs[i] = types.NewCommitSigAbsent()
			continue
		}
		v := &types.Vote{Type: cmtproto.PrecommitType, Height: height, Round: 0, BlockID: id, Timestamp: time.Unix(1700000000+height, 0).UTC(), ValidatorAddress: val.Address, ValidatorIndex: int32(i)}
		signature, err := keys[string(val.Address)].Sign(types.VoteSignBytes(chain, v.ToProto()))
		if err != nil {
			t.Fatal(err)
		}
		v.Signature = signature
		sigs[i] = v.CommitSig()
	}
	c := &types.Commit{Height: height, Round: 0, BlockID: id, Signatures: sigs}
	if err := vals.VerifyCommitLight(chain, id, height, c); err != nil {
		t.Fatal(err)
	}
	return c
}

func block(t *testing.T, height int64, tx types.Tx, last *types.Commit, vals *types.ValidatorSet) *types.Block {
	t.Helper()
	b := types.MakeBlock(height, []types.Tx{tx}, last, nil)
	b.ChainID = chain
	b.Time = time.Unix(1700000000+height, 0).UTC()
	b.ValidatorsHash = vals.Hash()
	b.NextValidatorsHash = vals.Hash()
	b.ProposerAddress = vals.Proposer.Address
	if height > 1 {
		b.LastBlockID = last.BlockID
	}
	if err := b.ValidateBasic(); err != nil {
		t.Fatal(err)
	}
	return b
}

func adapt(t *testing.T, p *chameleon.Public, signers []chameleon.Signer, ctx, old, next []byte, c chameleon.Commitment, r chameleon.Opening) chameleon.Opening {
	t.Helper()
	var shares []chameleon.Contribution
	for i := 0; i < 3; i++ {
		s, err := signers[i].Adapt(ctx, old, next, c, r)
		if err != nil {
			t.Fatal(err)
		}
		shares = append(shares, s)
	}
	r2, err := p.Combine(ctx, old, next, c, r, shares)
	if err != nil {
		t.Fatal(err)
	}
	return r2
}

func TestRealBlockStoreRewriteAndOriginalReplay(t *testing.T) {
	p, signers, vals, keys := committee(t)
	originalRef, debitRef := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	ctx := []byte("input/network/owner/immutable-core/0")
	c, r, err := p.Commit(ctx, originalRef)
	if err != nil {
		t.Fatal(err)
	}
	owner := ed25519.GenPrivKey()
	fixed := append(bytes.Repeat([]byte{42}, 70000), c[:]...)
	ownerSignature, err := owner.Sign(fixed)
	if err != nil {
		t.Fatal(err)
	}
	fixed = append(fixed, ownerSignature...)
	originalTx := types.EncodeRedactableTx(fixed, append(bytes.Clone(originalRef), r[:]...))
	b := block(t, 1, originalTx, &types.Commit{}, vals)
	initial, err := b.MakePartSet(types.BlockPartSizeBytes)
	if err != nil {
		t.Fatal(err)
	}
	id := types.BlockID{Hash: b.Hash(), PartSetHeader: initial.Header()}
	cert := commit(t, 1, id, vals, keys)
	db, err := dbm.NewDB("redaction", dbm.GoLevelDBBackend, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bs := store.NewBlockStore(db)
	bs.SaveBlock(b, initial, cert)

	r2 := adapt(t, p, signers, ctx, originalRef, debitRef, c, r)
	repairedTx := types.EncodeRedactableTx(fixed, append(bytes.Clone(debitRef), r2[:]...))
	if !bytes.Equal(originalTx.Hash(), repairedTx.Hash()) {
		t.Fatal("transaction identity changed")
	}
	pb, _ := b.ToProto()
	pb.Data.Txs[0] = repairedTx
	repaired, err := types.BlockFromProto(pb)
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := pb.Marshal()
	openings := make([]chameleon.Opening, initial.Total())
	for i := range openings {
		oldPart := initial.GetPart(i)
		start, end := i*int(types.BlockPartSizeBytes), min((i+1)*int(types.BlockPartSizeBytes), len(wire))
		partCtx := types.RedactionContext(1, uint32(i))
		partC, err := p.Digest(partCtx, oldPart.Bytes, oldPart.Redaction.Opening)
		if err != nil {
			t.Fatal(err)
		}
		openings[i] = adapt(t, p, signers, partCtx, oldPart.Bytes, wire[start:end], partC, oldPart.Redaction.Opening)
	}
	parts, err := types.NewRedactablePartSet(wire, types.BlockPartSizeBytes, 1, 1, openings)
	if err != nil {
		t.Fatal(err)
	}
	if !parts.Header().Equals(id.PartSetHeader) || !bytes.Equal(repaired.Hash(), id.Hash) {
		t.Fatal("complete BlockID changed")
	}
	if err = vals.VerifyCommitLight(chain, types.BlockID{Hash: repaired.Hash(), PartSetHeader: parts.Header()}, 1, cert); err != nil {
		t.Fatal(err)
	}
	proof := repaired.Data.Txs.Proof(0)
	if err := proof.Validate(b.DataHash); err != nil {
		t.Fatal(err)
	}
	if !owner.PubKey().VerifySignature(fixed[:len(fixed)-64], ownerSignature) {
		t.Fatal("owner signature no longer valid")
	}

	// Authorize exactly these new bytes in a subsequent immutable block.
	digest := sha256.Sum256(wire)
	repairCommand := append([]byte("RepairInput:debit=100;base=0;next=1;body="), digest[:]...)
	rb := block(t, 2, repairCommand, cert, vals)
	rparts, _ := rb.MakePartSet(types.BlockPartSizeBytes)
	rid := types.BlockID{Hash: rb.Hash(), PartSetHeader: rparts.Header()}
	rcert := commit(t, 2, rid, vals, keys)
	bs.SaveBlock(rb, rparts, rcert)
	authorize := func(old, next *types.Block) error {
		if err := vals.VerifyCommitLight(chain, rid, 2, rcert); err != nil {
			return err
		}
		actual, _ := next.ToProto()
		raw, _ := actual.Marshal()
		if sha256.Sum256(raw) != digest || !bytes.Equal(next.Data.Txs[0], repairedTx) || !p.Verify(ctx, debitRef, c, r2) {
			return errors.New("unauthorized repair")
		}
		return nil
	}
	if err := bs.ReviseBlock(1, 0, parts, nil); err == nil {
		t.Fatal("missing authorization accepted")
	}
	if err := bs.ReviseBlock(1, 0, parts, authorize); err != nil {
		t.Fatal(err)
	}
	if err := bs.ReviseBlock(1, 0, parts, authorize); err != nil {
		t.Fatalf("idempotent replay failed: %v", err)
	}
	if err := bs.ReviseBlock(1, 2, parts, authorize); err == nil {
		t.Fatal("wrong predecessor accepted")
	}
	// Reopen the actual store, not an in-memory projection or mocked repository.
	bs = store.NewBlockStore(db)
	got := bs.LoadBlock(1)
	if bytes.Equal(got.Data.Txs[0], originalTx) || !bytes.Equal(got.Data.Txs[0], repairedTx) {
		t.Fatal("stored transaction bytes were not rewritten")
	}
	loadedPart := bs.LoadBlockPart(1, 0)
	if loadedPart.Redaction.Revision != 1 {
		t.Fatal("part metadata not persisted")
	}
	origin, err := bs.LoadOriginalBlock(1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(origin.Data.Txs[0], originalTx) {
		t.Fatal("lost original replay bytes")
	}
	// Catchup must feed the original representation through the same live gate.
	catchup := types.NewPartSetFromHeader(id.PartSetHeader)
	for i := 0; i < int(initial.Total()); i++ {
		part, err := bs.OriginalBlockPart(1, i)
		if err != nil {
			t.Fatal(err)
		}
		wirePart, err := part.ToProto()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := consensus.MsgFromProto(&cmtcons.BlockPart{Height: 1, Part: *wirePart})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := catchup.AddPart(decoded.(*consensus.BlockPartMessage).Part); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(part.Bytes, initial.GetPart(i).Bytes) {
			t.Fatal("catchup served revised execution bytes")
		}
	}
	if !catchup.IsComplete() {
		t.Fatal("original catchup part set is incomplete")
	}
	if !bytes.Equal(bs.LoadBlock(2).Data.Txs[0], repairCommand) {
		t.Fatal("repair command was altered")
	}
	// Real protobuf part roundtrip and accumulation, using the original root.
	received := types.NewPartSetFromHeader(id.PartSetHeader)
	for i := 0; i < int(parts.Total()); i++ {
		pb, _ := bs.LoadBlockPart(1, i).ToProto()
		raw, _ := pb.Marshal()
		decoded := new(cmtproto.Part)
		if err := decoded.Unmarshal(raw); err != nil {
			t.Fatal(err)
		}
		part, err := types.PartFromProto(decoded)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := received.AddPart(part); err != nil {
			t.Fatal(err)
		}
	}
	gotWire, _ := io.ReadAll(received.GetReader())
	if !bytes.Equal(gotWire, wire) {
		t.Fatal("sync bytes differ")
	}
	mixed := types.NewPartSetFromHeader(id.PartSetHeader)
	if _, err := mixed.AddPart(initial.GetPart(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := mixed.AddPart(parts.GetPart(1)); err == nil {
		t.Fatal("mixed revisions accepted")
	}
	t.Logf("rewrote real stored tx and %d parts; original 3/4 commit, owner signature and inclusion proof valid", parts.Total())
}
