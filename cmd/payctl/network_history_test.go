//go:build comet_v3

package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	cmted "github.com/cometbft/cometbft/crypto/ed25519"
	cmtstore "github.com/cometbft/cometbft/store"
	ct "github.com/cometbft/cometbft/types"
	cfg "utxo/cmd/internal/config"
)

// TestNetworkHistoryExport reads a stopped experiment, never a live node DB.
// Proposer priorities advance once per height in the fixed validator set;
// round increments are local to the consensus round, not carried to the next height.
func TestNetworkHistoryExport(t *testing.T) {
	root := os.Getenv("UTXO_NETWORK_HISTORY")
	if root == "" {
		t.Skip("offline retained experiment audit")
	}
	var n cfg.Network
	if err := cfg.Read(filepath.Join(root, "config", "network.json"), &n); err != nil {
		t.Fatal(err)
	}
	trust, err := n.Trust()
	if err != nil {
		t.Fatal(err)
	}
	db, err := dbm.NewDB("blockstore", dbm.GoLevelDBBackend, filepath.Join(root, "committee0", "comet", "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	blocks := cmtstore.NewBlockStore(db)
	priorities := trust.Validators.Copy()
	type row struct {
		Height                      int64
		Round                       int32
		RoundZeroProposer, Proposer string
		CommitNS                    int64
		Signers                     []string
		Transactions                int
	}
	rows := make([]row, 0, blocks.Height())
	for h := int64(1); h <= blocks.Height(); h++ {
		meta := blocks.LoadBlockMeta(h)
		commit := blocks.LoadBlockCommit(h)
		if commit == nil {
			commit = blocks.LoadSeenCommit(h)
		}
		if meta == nil || commit == nil {
			t.Fatalf("missing history %d", h)
		}
		if err := trust.Validators.VerifyCommitLight(n.ChainID, meta.BlockID, h, commit); err != nil {
			t.Fatal(err)
		}
		r := row{Height: h, Round: commit.Round, RoundZeroProposer: hex.EncodeToString(priorities.GetProposer().Address), Proposer: hex.EncodeToString(meta.Header.ProposerAddress), Transactions: int(meta.NumTxs)}
		// A proposal can originate in an earlier valid round than the commit.
		candidates := priorities.Copy()
		found := false
		for round := int32(0); round <= commit.Round; round++ {
			if bytes.Equal(candidates.GetProposer().Address, meta.Header.ProposerAddress) {
				found = true
			}
			candidates.IncrementProposerPriority(1)
		}
		if !found {
			t.Fatalf("unexpected proposer at %d", h)
		}
		for _, sig := range commit.Signatures {
			if sig.BlockIDFlag == ct.BlockIDFlagCommit {
				r.Signers = append(r.Signers, hex.EncodeToString(sig.ValidatorAddress))
				if ns := sig.Timestamp.UnixNano(); ns > r.CommitNS {
					r.CommitNS = ns
				}
			}
		}
		rows = append(rows, r)
		priorities.IncrementProposerPriority(1)
	}
	out := struct {
		Committee3 string
		Blocks     []row
	}{hex.EncodeToString(cmted.PubKey(n.Committee[3][:]).Address()), rows}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("UTXO_NETWORK_HISTORY_OUT"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("verified %d committed blocks", len(rows))
}
