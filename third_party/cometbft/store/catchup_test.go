package store

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"os"
	"os/exec"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/cometbft/cometbft/types"
	cmttime "github.com/cometbft/cometbft/types/time"
	"github.com/stretchr/testify/require"
	"utxo/crypto/chameleon"
)

type catchupReadDB struct {
	dbm.DB
	gets []string
}

func (d *catchupReadDB) Get(key []byte) ([]byte, error) {
	d.gets = append(d.gets, string(key))
	return d.DB.Get(key)
}

func catchupFixture(t *testing.T) (*BlockStore, *catchupReadDB, *types.Block, *types.PartSet) {
	t.Helper()
	state, _, cleanup := makeStateAndBlockStore()
	t.Cleanup(cleanup)
	db := &catchupReadDB{DB: dbm.NewMemDB()}
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bs := NewBlockStore(db)
	block, err := state.MakeBlock(1, []types.Tx{bytes.Repeat([]byte{3}, int(types.BlockPartSizeBytes)*4)}, new(types.Commit), nil, state.Validators.GetProposer().Address)
	require.NoError(t, err)
	parts, err := block.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	require.Greater(t, parts.Total(), uint32(3))
	bs.SaveBlockWithExtendedCommit(block, parts, makeTestExtCommit(block.Height, cmttime.Now()))
	return bs, db, block, parts
}

func TestCatchupCandidateReadsOneUnchangedPart(t *testing.T) {
	bs, db, block, parts := catchupFixture(t)
	// The old path rebuilds a canonical PartSet from the complete original block.
	original, err := bs.LoadOriginalBlock(block.Height)
	require.NoError(t, err)
	rebuilt, err := original.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)
	db.gets = nil
	part, err := bs.OriginalBlockPart(block.Height, 2)
	require.NoError(t, err)
	require.Equal(t, rebuilt.GetPart(2), part) // bytes, proof, and CH metadata if enabled.
	require.Equal(t, []string{string(originalKey(1)), string(calcBlockMetaKey(1)), string(calcBlockPartKey(1, 2))}, db.gets)
	for _, index := range []int{-1, int(parts.Total())} {
		_, err := bs.OriginalBlockPart(block.Height, index)
		require.ErrorIs(t, err, types.ErrRedaction)
	}
	missing, err := bs.OriginalBlockPart(99, -1)
	require.NoError(t, err)
	require.Nil(t, missing)
}

func TestCatchupCandidateRewrittenBlockStillServesOriginal(t *testing.T) {
	bs, db, block, originalParts := catchupFixture(t)
	original, err := block.ToProto()
	require.NoError(t, err)
	raw, err := original.Marshal()
	require.NoError(t, err)
	// Seed the persisted original/current layout; repair authorization is tested
	// separately by the redaction suite. This test checks the reader's selection.
	require.NoError(t, db.Set(originalKey(1), raw))
	changedPart := *originalParts.GetPart(2)
	changedPart.Bytes = bytes.Repeat([]byte{9}, len(changedPart.Bytes))
	changed, err := changedPart.ToProto()
	require.NoError(t, err)
	changedBytes, err := changed.Marshal()
	require.NoError(t, err)
	require.NoError(t, db.Set(calcBlockPartKey(1, 2), changedBytes))
	require.NotEqual(t, originalParts.GetPart(2).Bytes, bs.LoadBlockPart(1, 2).Bytes)
	db.gets = nil
	part, err := bs.OriginalBlockPart(1, 2)
	require.NoError(t, err)
	require.Equal(t, originalParts.GetPart(2), part)
	require.Equal(t, []string{string(originalKey(1))}, db.gets)
	_, err = bs.OriginalBlockPart(1, int(originalParts.Total()))
	require.ErrorIs(t, err, types.ErrRedaction)
}

func TestCatchupCandidateRedactableSubprocess(t *testing.T) {
	// ConfigureRedaction is process-global, so leave the other store tests unchanged.
	if os.Getenv("UTXO_CATCHUP_CH_TEST") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCatchupCandidateRedactableSubprocess$")
		cmd.Env = append(os.Environ(), "UTXO_CATCHUP_CH_TEST=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
		return
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	public, err := chameleon.NewPublic(&key.PublicKey)
	require.NoError(t, err)
	require.NoError(t, types.ConfigureRedaction("catchup-candidate", public))
	t.Run("unchanged", TestCatchupCandidateReadsOneUnchangedPart)
	t.Run("rewritten", TestCatchupCandidateRewrittenBlockStillServesOriginal)
}
