package consensus

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"testing"

	"github.com/cometbft/cometbft/abci/example/kvstore"
	abci "github.com/cometbft/cometbft/abci/types"
	cstypes "github.com/cometbft/cometbft/consensus/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/types"
	"utxo/crypto/chameleon"
)

// Two authentic original part sets, equal stable hashes, distinct mutable bytes.
// This tests consensus identity handling, not application transaction validity.
type identityApplication struct {
	abci.Application
	observe func(*abci.RequestFinalizeBlock)
}

func (a identityApplication) FinalizeBlock(ctx context.Context, r *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	a.observe(r)
	return a.Application.FinalizeBlock(ctx, r)
}

func identityFixture(t *testing.T, observers ...func(*abci.RequestFinalizeBlock)) (*State, []*validatorStub, *types.Block, *types.PartSet, *types.Block, *types.PartSet) {
	t.Helper()
	var app abci.Application = kvstore.NewInMemoryApplication()
	if len(observers) > 0 {
		app = identityApplication{Application: app, observe: observers[0]}
	}
	cs, validators := randStateWithApp(4, app)
	t.Cleanup(func() { _ = cs.eventBus.Stop() })
	raw, err := os.ReadFile("../../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(encoded.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := chameleon.NewPublic(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = types.ConfigureRedaction(cs.state.ChainID, pub); err != nil {
		t.Fatal(err)
	}
	base, err := cs.createProposalBlock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	makeBlock := func(mutable string) (*types.Block, *types.PartSet) {
		pb, err := base.ToProto()
		if err != nil {
			t.Fatal(err)
		}
		block, err := types.BlockFromProto(pb)
		if err != nil {
			t.Fatal(err)
		}
		block.Data = types.Data{Txs: types.Txs{types.EncodeRedactableTx([]byte("fixed="), []byte(mutable))}}
		block.Header.DataHash = nil
		_ = block.Hash()
		parts, err := block.MakePartSet(types.BlockPartSizeBytes)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < int(parts.Total()); i++ {
			if err = parts.GetPart(i).ValidateOriginal(block.Height); err != nil {
				t.Fatal(err)
			}
		}
		return block, parts
	}
	a, pa := makeBlock("aaa")
	b, pb := makeBlock("bbb")
	if !bytes.Equal(a.Hash(), b.Hash()) || pa.HasHeader(pb.Header()) {
		t.Fatal("not distinct original identities")
	}
	for _, v := range validators {
		v.Height, v.Round = cs.Height, cs.Round
	}
	return cs, validators, a, pa, b, pb
}

func identityVotes(t *testing.T, cs *State, validators []*validatorStub, kind cmtproto.SignedMsgType, b *types.Block, parts *types.PartSet) []*types.Vote {
	t.Helper()
	extensions := kind == cmtproto.PrecommitType && cs.state.ConsensusParams.ABCI.VoteExtensionsEnabled(cs.Height)
	return signVotes(kind, b.Hash(), parts.Header(), extensions, validators[1:]...)
}

// The fork's redaction key is startup-only process state. Isolate these tests
// from upstream tests that deliberately build non-redactable networks.
func TestStableHashIdentity(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestStableHashIdentityCases$", "-test.v")
	cmd.Env = append(os.Environ(), "NEXT_COMET_IDENTITY_TEST=1")
	output, err := cmd.CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatal(err)
	}
}

func TestStableHashIdentityCases(t *testing.T) {
	if os.Getenv("NEXT_COMET_IDENTITY_TEST") != "1" {
		t.Skip("run in isolated key-configured process")
	}
	t.Run("commit_fetch", identityCommitFetch)
	t.Run("finalize_waits", identityTryFinalizeWaits)
	t.Run("precommit", identityPrecommitDoesNotRelockAlias)
	t.Run("valid_block", identityValidBlockNotAliased)
	t.Run("pol_rounds", identityPOLRounds)
	t.Run("exact_precommit", identityExactPrecommit)
	t.Run("proposal_binding", identityProposalBinding)
	t.Run("qc_without_matching_proposal", identityQCWithoutMatchingProposal)
	t.Run("late_parts_after_nil_precommit", identityLatePartsAfterNilPrecommit)
}

func identityCommitFetch(t *testing.T) {
	var finalized [][]byte
	cs, vals, a, pa, b, pb := identityFixture(t, func(r *abci.RequestFinalizeBlock) {
		if len(r.Txs) != 1 {
			t.Fatal("unexpected execution body")
		}
		finalized = append(finalized, bytes.Clone(r.Txs[0]))
	})
	for _, vote := range identityVotes(t, cs, vals, cmtproto.PrecommitType, b, pb) {
		if added, err := cs.Votes.Precommits(cs.Round).AddVote(vote); err != nil || !added {
			t.Fatalf("vote: %v %v", added, err)
		}
	}
	cs.ProposalBlock, cs.ProposalBlockParts = a, pa
	cs.LockedBlock, cs.LockedBlockParts, cs.LockedRound = a, pa, cs.Round
	cs.enterCommit(cs.Height, cs.Round)
	if cs.ProposalBlock != nil || !cs.ProposalBlockParts.HasHeader(pb.Header()) || cs.ProposalBlockParts.IsComplete() {
		t.Fatal("must fetch the exact committed identity instead of executing its hash alias")
	}
	if len(finalized) != 0 {
		t.Fatal("alias reached FinalizeBlock before fetching committed bytes")
	}
	// Delayed parts from A cannot satisfy B's root. B needs no Proposal message:
	// the already verified commit identifies what to fetch and apply.
	height, round := cs.Height, cs.Round
	if _, err := cs.addProposalBlockPart(&BlockPartMessage{Height: height, Round: round, Part: pa.GetPart(0)}, "alias-peer"); err == nil {
		t.Fatal("mixed an alias part into the committed block")
	}
	for i := 0; i < int(pb.Total()); i++ {
		msg := &BlockPartMessage{Height: height, Round: round, Part: pb.GetPart(i)}
		if err := msg.ValidateBasic(); err != nil {
			t.Fatal(err)
		}
		if _, err := cs.addProposalBlockPart(msg, "commit-peer"); err != nil {
			t.Fatal(err)
		}
		if added, err := cs.addProposalBlockPart(msg, "duplicate-peer"); err != nil || added {
			t.Fatalf("duplicate part %v %v", added, err)
		}
	}
	cs.handleCompleteProposal(height)
	if cs.Height != height+1 || cs.blockStore.Height() != height {
		t.Fatal("exact committed block did not advance")
	}
	stored := cs.blockStore.LoadBlock(height)
	meta := cs.blockStore.LoadBlockMeta(height)
	if !bytes.Equal(stored.Txs[0], b.Txs[0]) || !meta.BlockID.PartSetHeader.Equals(pb.Header()) {
		t.Fatal("applied/stored a different block")
	}
	if len(finalized) != 1 || !bytes.Equal(finalized[0], b.Txs[0]) {
		t.Fatal("FinalizeBlock did not receive exactly B once")
	}
	for _, vote := range signVotes(cmtproto.PrecommitType, b.Hash(), pb.Header(), true, vals[1:]...) {
		if _, err := cs.addVote(vote, "duplicate-commit-peer"); err != nil {
			t.Fatal(err)
		}
	}
	if len(finalized) != 1 {
		t.Fatal("duplicate commit reapplied block")
	}
}

func identityTryFinalizeWaits(t *testing.T) {
	cs, vals, a, pa, b, pb := identityFixture(t)
	for _, vote := range identityVotes(t, cs, vals, cmtproto.PrecommitType, b, pb) {
		if added, err := cs.Votes.Precommits(cs.Round).AddVote(vote); err != nil || !added {
			t.Fatalf("vote: %v %v", added, err)
		}
	}
	cs.ProposalBlock, cs.ProposalBlockParts = a, pa
	cs.CommitRound, cs.Step = cs.Round, cstypes.RoundStepCommit
	height := cs.Height
	cs.tryFinalizeCommit(height)
	if cs.Height != height || cs.blockStore.Height() != 0 {
		t.Fatal("executed a different identity")
	}
}

func identityPrecommitDoesNotRelockAlias(t *testing.T) {
	for _, locked := range []bool{false, true} {
		cs, vals, a, pa, b, pb := identityFixture(t)
		for _, vote := range identityVotes(t, cs, vals, cmtproto.PrevoteType, b, pb) {
			if added, err := cs.Votes.Prevotes(cs.Round).AddVote(vote); err != nil || !added {
				t.Fatalf("vote: %v %v", added, err)
			}
		}
		cs.ProposalBlock, cs.ProposalBlockParts = a, pa
		if locked {
			cs.LockedBlock, cs.LockedBlockParts, cs.LockedRound = a, pa, cs.Round
		}
		cs.enterPrecommit(cs.Height, cs.Round)
		if cs.LockedBlock != nil || cs.ProposalBlock != nil || !cs.ProposalBlockParts.HasHeader(pb.Header()) {
			t.Fatal("locked or retained a hash alias of the polka")
		}
		select {
		case msg := <-cs.internalMsgQueue:
			vote, ok := msg.Msg.(*VoteMessage)
			if !ok || !vote.Vote.BlockID.IsZero() {
				t.Fatal("must precommit nil without the exact block")
			}
		default:
			t.Fatal("missing nil precommit")
		}
	}
}

func identityValidBlockNotAliased(t *testing.T) {
	cs, vals, a, pa, b, pb := identityFixture(t)
	for _, vote := range identityVotes(t, cs, vals, cmtproto.PrevoteType, b, pb) {
		if added, err := cs.Votes.Prevotes(cs.Round).AddVote(vote); err != nil || !added {
			t.Fatalf("vote: %v %v", added, err)
		}
	}
	cs.ProposalBlock, cs.ProposalBlockParts = a, pa
	cs.Step = cstypes.RoundStepPrecommit
	cs.handleCompleteProposal(cs.Height)
	if cs.ValidBlock != nil {
		t.Fatal("promoted a different identity to ValidBlock")
	}
}

func identityPOLRounds(t *testing.T) {
	for _, voteRound := range []int32{0, 1, 2} {
		cs, vals, a, pa, b, pb := identityFixture(t)
		cs.Round, cs.Step = 2, cstypes.RoundStepPrecommit
		cs.Votes.SetRound(2)
		cs.LockedBlock, cs.LockedBlockParts, cs.LockedRound = a, pa, 1
		cs.ProposalBlock, cs.ProposalBlockParts = a, pa
		for _, v := range vals {
			v.Round = voteRound
		}
		for _, vote := range identityVotes(t, cs, vals, cmtproto.PrevoteType, b, pb) {
			if added, err := cs.addVote(vote, "pol-peer"); err != nil || !added {
				t.Fatalf("round %d: %v %v", voteRound, added, err)
			}
		}
		if voteRound <= 1 {
			if cs.LockedBlock != a || cs.LockedRound != 1 {
				t.Fatal("old or same-round POL unlocked a newer lock")
			}
		} else if cs.LockedBlock != nil || cs.ValidBlock != nil || cs.ProposalBlock != nil || !cs.ProposalBlockParts.HasHeader(pb.Header()) {
			t.Fatal("new POL did not unlock the alias and fetch the exact candidate")
		}
	}
}

func identityExactPrecommit(t *testing.T) {
	cs, vals, _, _, b, pb := identityFixture(t)
	for _, vote := range identityVotes(t, cs, vals, cmtproto.PrevoteType, b, pb) {
		if added, err := cs.Votes.Prevotes(cs.Round).AddVote(vote); err != nil || !added {
			t.Fatalf("vote: %v %v", added, err)
		}
	}
	cs.ProposalBlock, cs.ProposalBlockParts = b, pb
	cs.enterPrecommit(cs.Height, cs.Round)
	if cs.LockedBlock != b || !cs.LockedBlockParts.HasHeader(pb.Header()) {
		t.Fatal("exact candidate did not lock")
	}
	select {
	case msg := <-cs.internalMsgQueue:
		vote, ok := msg.Msg.(*VoteMessage)
		if !ok || !vote.Vote.BlockID.Equals(types.BlockID{Hash: b.Hash(), PartSetHeader: pb.Header()}) {
			t.Fatal("wrong precommit identity")
		}
	default:
		t.Fatal("missing precommit")
	}
}

func identityProposalBinding(t *testing.T) {
	cs, vals, a, pa, b, pb := identityFixture(t)
	idA := types.BlockID{Hash: a.Hash(), PartSetHeader: pa.Header()}
	idB := types.BlockID{Hash: b.Hash(), PartSetHeader: pb.Header()}
	cs.Proposal = types.NewProposal(cs.Height, cs.Round, -1, idA)
	cs.ProposalBlock, cs.ProposalBlockParts = b, pb
	if cs.isProposalComplete() {
		t.Fatal("different full identity counted as complete proposal")
	}
	cs.defaultDoPrevote(cs.Height, cs.Round)
	select {
	case msg := <-cs.internalMsgQueue:
		vote, ok := msg.Msg.(*VoteMessage)
		if !ok || !vote.Vote.BlockID.IsZero() {
			t.Fatal("timeout prevote used another proposal's identity")
		}
	default:
		t.Fatal("missing nil prevote")
	}
	cs.ProposalBlock, cs.ProposalBlockParts = a, pa
	if !cs.isProposalComplete() {
		t.Fatal("matching proposal incomplete")
	}
	cs.Round = 1
	cs.Votes.SetRound(1)
	cs.Proposal = types.NewProposal(cs.Height, 1, 0, idA)
	for _, vote := range identityVotes(t, cs, vals, cmtproto.PrevoteType, b, pb) {
		if added, err := cs.Votes.Prevotes(0).AddVote(vote); err != nil || !added {
			t.Fatalf("POL: %v %v", added, err)
		}
	}
	if cs.isProposalComplete() {
		t.Fatal("unrelated POL authorized proposal")
	}
	cs.Proposal = types.NewProposal(cs.Height, 1, 0, idB)
	cs.ProposalBlock, cs.ProposalBlockParts = b, pb
	if !cs.isProposalComplete() {
		t.Fatal("matching POL/proposal incomplete")
	}
}

func identityQCWithoutMatchingProposal(t *testing.T) {
	for _, partsFirst := range []bool{false, true} {
		cs, vals, a, pa, b, pb := identityFixture(t)
		cs.Step = cstypes.RoundStepPrevote
		cs.Proposal = types.NewProposal(cs.Height, cs.Round, -1, types.BlockID{Hash: a.Hash(), PartSetHeader: pa.Header()})
		if partsFirst {
			cs.ProposalBlock, cs.ProposalBlockParts = b, pb
		} else {
			cs.ProposalBlock, cs.ProposalBlockParts = a, pa
		}
		for _, vote := range identityVotes(t, cs, vals, cmtproto.PrevoteType, b, pb) {
			if added, err := cs.addVote(vote, "qc-peer"); err != nil || !added {
				t.Fatalf("QC: %v %v", added, err)
			}
		}
		if !partsFirst {
			for i := 0; i < int(pb.Total()); i++ {
				if _, err := cs.addProposalBlockPart(&BlockPartMessage{Height: cs.Height, Round: cs.Round, Part: pb.GetPart(i)}, "qc-peer"); err != nil {
					t.Fatal(err)
				}
			}
			cs.handleCompleteProposal(cs.Height)
		}
		if cs.LockedBlock == nil || !cs.LockedBlockParts.HasHeader(pb.Header()) || cs.Step != cstypes.RoundStepPrecommit {
			t.Fatalf("partsFirst=%v: matching QC/candidate stalled behind old proposal", partsFirst)
		}
	}
}

func identityLatePartsAfterNilPrecommit(t *testing.T) {
	cs, vals, a, pa, b, pb := identityFixture(t)
	cs.ProposalBlock, cs.ProposalBlockParts = a, pa
	for _, vote := range identityVotes(t, cs, vals, cmtproto.PrevoteType, b, pb) {
		if added, err := cs.Votes.Prevotes(cs.Round).AddVote(vote); err != nil || !added {
			t.Fatalf("vote: %v %v", added, err)
		}
	}
	cs.enterPrecommit(cs.Height, cs.Round)
	select {
	case msg := <-cs.internalMsgQueue:
		vote, ok := msg.Msg.(*VoteMessage)
		if !ok || !vote.Vote.BlockID.IsZero() {
			t.Fatal("expected first nil precommit")
		}
	default:
		t.Fatal("missing first precommit")
	}
	for i := 0; i < int(pb.Total()); i++ {
		if _, err := cs.addProposalBlockPart(&BlockPartMessage{Height: cs.Height, Round: cs.Round, Part: pb.GetPart(i)}, "late-peer"); err != nil {
			t.Fatal(err)
		}
	}
	cs.handleCompleteProposal(cs.Height)
	if cs.LockedBlock != nil || cs.Step != cstypes.RoundStepPrecommit {
		t.Fatal("late parts changed a completed precommit step")
	}
	select {
	case <-cs.internalMsgQueue:
		t.Fatal("late parts produced a second precommit")
	default:
	}
}
