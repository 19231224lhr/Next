//go:build comet_v3

package redaction_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	dbm "github.com/cometbft/cometbft-db"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtstore "github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"utxo/crypto/chameleon"
	apppkg "utxo/internal/committee"
	"utxo/internal/redaction"
	"utxo/internal/rules"
	"utxo/internal/state"
	appstore "utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestPaymentRepairMonetaryReplay(t *testing.T) {
	pub, signers, vals, keys := committee(t)
	f := testkit.NewFixture(chain, "direct-org", 1)
	f.EnableDirect()
	pemBytes, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	pemBlock, _ := pem.Decode(pemBytes)
	rsaKey, err := x509.ParsePKCS1PrivateKey(pemBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	settings := rules.DirectSettings{Modulus: rsaKey.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	policy, err := settings.Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if err != nil {
		t.Fatal(err)
	}
	cfg := apppkg.EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Direct: &settings, Accounts: []apppkg.GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetCAL, Balance: 1000000000}, {Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1000000000}}}
	disk, err := dbm.NewDB("blocks", dbm.GoLevelDBBackend, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	blocks := cmtstore.NewBlockStore(disk)
	newApp := func(memory bool) (appstore.Store, *apppkg.App, *apppkg.Engine) {
		path := filepath.Join(t.TempDir(), "committee.db")
		id := appstore.Identity{Network: cfg.Network.String(), Role: "committee", Node: "test", Schema: 4}
		var db appstore.Store
		var err error
		if memory {
			db, err = appstore.OpenEphemeral(path, id)
		} else {
			db, err = appstore.Open(path, id)
		}
		if err != nil {
			t.Fatal(err)
		}
		engine, err := apppkg.NewEngine(cfg, db)
		if err != nil {
			t.Fatal(err)
		}
		if err = engine.EnableRepair(blocks); err != nil {
			t.Fatal(err)
		}
		app, err := apppkg.NewTimedApp(chain, db, engine.Check, engine.ExecuteAt, engine.BeginBlock)
		if err != nil {
			t.Fatal(err)
		}
		return db, app, engine
	}
	db, app, warmEngine := newApp(false)
	defer db.Close()
	ctx := context.Background()
	parent, err := f.FastTransaction(0, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := f.DirectCertificate(parent, policy)
	if err != nil {
		t.Fatal(err)
	}
	body := parent.Body
	body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: pc.Summary.OutputID(0), Evidence: protocol.Hash(pc.QC.Fact)}}
	body.Nonce[0] = 40
	body.Intent = body.IntentID()
	child, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: parent.Body.Outputs[0]}}, pub)
	if err != nil {
		t.Fatal(err)
	}
	child.Auth = []protocol.OwnerAuth{protocol.SignOwner(child.ID(), f.Owner)}
	cc, err := f.DirectCertificate(child, policy)
	if err != nil {
		t.Fatal(err)
	}
	childRaw, err := (protocol.DirectPayment{Tx: child, Certificate: cc, InputCertificates: []protocol.InputCertificate{{Certificate: pc, Index: 0}}}).Submission().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var roots [][]byte
	var ledgers [][]state.Entry
	snapshot := func(db appstore.Store) []state.Entry {
		var rows []state.Entry
		var after []byte
		for {
			page, err := appstore.Scan(db, nil, after, 1024)
			if err != nil {
				t.Fatal(err)
			}
			if len(page) == 0 {
				return rows
			}
			rows = append(rows, page...)
			after = page[len(page)-1].Key
		}
	}
	last := &types.Commit{}
	execute := func(app *apppkg.App, b *types.Block) []byte {
		txs := make([][]byte, len(b.Data.Txs))
		for i, tx := range b.Data.Txs {
			txs[i] = tx
		}
		r, err := app.FinalizeBlock(ctx, &abci.RequestFinalizeBlock{Height: b.Height, Hash: b.Hash(), Time: b.Time, Txs: txs})
		if err != nil {
			t.Fatal(err)
		}
		for _, tx := range r.TxResults {
			if tx.Code != 0 {
				t.Fatalf("transaction rejected: %+v", tx)
			}
		}
		if _, err = app.Commit(ctx, &abci.RequestCommit{}); err != nil {
			t.Fatal(err)
		}
		return bytes.Clone(r.AppHash)
	}
	appendBlock := func(height, stamp int64, raw []byte) *types.Block {
		b := block(t, height, raw, last, vals)
		b.Time = time.Unix(stamp, 0).UTC()
		if raw == nil {
			b = types.MakeBlock(height, nil, last, nil)
			b.ChainID = chain
			b.Time = time.Unix(stamp, 0).UTC()
			b.ValidatorsHash = vals.Hash()
			b.NextValidatorsHash = vals.Hash()
			b.ProposerAddress = vals.Proposer.Address
			b.LastBlockID = last.BlockID
		}
		parts, err := b.MakePartSet(types.BlockPartSizeBytes)
		if err != nil {
			t.Fatal(err)
		}
		id := types.BlockID{Hash: b.Hash(), PartSetHeader: parts.Header()}
		last = commit(t, height, id, vals, keys)
		blocks.SaveBlock(b, parts, last)
		roots = append(roots, execute(app, b))
		ledgers = append(ledgers, snapshot(db))
		return b
	}
	first := appendBlock(1, 1700000001, childRaw)
	originalID := blocks.LoadBlockMeta(1).BlockID
	appendBlock(2, 1700000002, nil)
	output := pc.Summary.OutputID(0)
	var command protocol.RepairInput
	var payment protocol.DirectSubmission
	var inputShares []chameleon.Contribution
	err = db.View(func(v state.ReadView) error {
		var err error
		command, payment, err = redaction.InputTarget(v, blocks, policy, output, 1700000032)
		if err != nil {
			return err
		}
		if _, err = redaction.InputShare(v, blocks, policy, signers[0], output, 1700000030); err == nil {
			t.Fatal("early repair share issued")
		}
		for i := 0; i < 3; i++ {
			s, err := redaction.InputShare(v, blocks, policy, signers[i], output, 1700000032)
			if err != nil {
				return err
			}
			inputShares = append(inputShares, s)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	command, err = redaction.ReplaceInput(policy, command, payment, inputShares)
	if err != nil {
		t.Fatal(err)
	}
	err = db.View(func(v state.ReadView) error {
		var shares [][]chameleon.Contribution
		for i := 0; i < 3; i++ {
			s, err := redaction.PartShares(v, blocks, policy, signers[i], command, 1700000032)
			if err != nil {
				return err
			}
			shares = append(shares, s)
		}
		var err error
		command, err = redaction.CompleteParts(v, blocks, policy, command, 1700000032, shares)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	repairID := protocol.RepairIdentity(f.Org.Network, output)
	// A valid chameleon opening is not sufficient authority: the exact target,
	// revision, replacement bytes and original PartSetHeader must all agree.
	for _, name := range []string{"output", "revision", "transaction", "part", "same-height", "owner-auth", "organization-auth", "input-certificate"} {
		t.Run("reject-"+name, func(t *testing.T) {
			bad := command
			bad.TransactionBytes = bytes.Clone(command.TransactionBytes)
			bad.Parts = append([]chameleon.Opening(nil), command.Parts...)
			height := int64(3)
			switch name {
			case "output":
				bad.Output[0] ^= 1
			case "revision":
				bad.Base++
			case "transaction":
				bad.TransactionBytes[len(bad.TransactionBytes)-1] ^= 1
			case "part":
				bad.Parts[0][0] ^= 1
			case "same-height":
				height = command.Height
			case "owner-auth", "organization-auth", "input-certificate":
				changed, err := protocol.DecodeDirectSubmission(bad.TransactionBytes)
				if err != nil {
					t.Fatal(err)
				}
				switch name {
				case "owner-auth":
					changed.Tx.Auth[0].Signature[0] ^= 1
				case "organization-auth":
					changed.Authorization.Votes[0].Signature[0] ^= 1
				case "input-certificate":
					changed.InputCertificates[0].Certificate.QC.Votes[0].Signature[0] ^= 1
				}
				funding := changed.Tx.Funding[command.Input]
				if !policy.Key.Verify(payment.Tx.FundingContext(int(command.Input), policy.Key.KeyID()), funding.ReferenceBytes(), payment.Tx.Commitments[command.Input], funding.Opening) {
					t.Fatal("test must retain a valid replacement opening")
				}
				if name == "owner-auth" {
					// The canonical encoder already rejects bad owner signatures.
					// Corrupt the received wire bytes to exercise the trust boundary.
					at := bytes.Index(bad.TransactionBytes, payment.Tx.Auth[0].Signature[:])
					if at < 0 {
						t.Fatal("owner signature not found")
					}
					bad.TransactionBytes[at] ^= 1
				} else {
					bad.TransactionBytes, err = changed.MarshalBinary()
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := db.View(func(v state.ReadView) error {
				tr, err := redaction.Execute(v, blocks, policy, bad, height, 1700000033)
				if err == nil || len(tr.Changes) != 0 {
					t.Fatalf("unauthorized repair returned changes: %v", err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err = db.View(func(v state.ReadView) error { return redaction.Materialize(v, blocks, repairID) }); err == nil {
		t.Fatal("uncommitted repair materialized")
	}
	repairRaw, err := command.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	parentRaw, err := (protocol.DirectPayment{Tx: parent, Certificate: pc}).Submission().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	for _, repairFirst := range []bool{false, true} {
		name := "same-block-parent-first"
		if repairFirst {
			name = "same-block-repair-first"
		}
		t.Run(name, func(t *testing.T) {
			forkDB, fork, _ := newApp(true)
			defer forkDB.Close()
			for height := int64(1); height <= 2; height++ {
				original, err := blocks.LoadOriginalBlock(height)
				if err != nil {
					t.Fatal(err)
				}
				execute(fork, original)
			}
			txs := [][]byte{parentRaw, repairRaw}
			if repairFirst {
				txs = [][]byte{repairRaw, parentRaw}
			}
			hash := protocol.Digest("E3-ORDER", []byte(name))
			result, err := fork.FinalizeBlock(ctx, &abci.RequestFinalizeBlock{Height: 3, Hash: hash[:], Time: time.Unix(1700000033, 0), Txs: txs})
			if err != nil {
				t.Fatal(err)
			}
			if result.TxResults[0].Code != 0 || (result.TxResults[1].Code == 0) != repairFirst {
				t.Fatalf("wrong order result: %+v", result.TxResults)
			}
			if _, err := fork.Commit(ctx, &abci.RequestCommit{}); err != nil {
				t.Fatal(err)
			}
			if err := forkDB.View(func(v state.ReadView) error {
				ob, _, err := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(output))
				if err != nil {
					return err
				}
				balance, _, err := state.Load[uint64](v, rules.AccountKey(f.Org.Org, protocol.AssetCAL))
				wantStatus, wantBalance := uint8(rules.DirectFulfilled), uint64(1000000000)
				if repairFirst {
					wantStatus, wantBalance = rules.DirectRepaired, 999999900
				}
				if ob.Status != wantStatus || balance != wantBalance {
					t.Fatalf("order changed accounting: status=%d balance=%d", ob.Status, balance)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
	appendBlock(3, 1700000033, repairRaw)
	checkObserved := func(materialized bool) {
		t.Helper()
		if err := db.View(func(v state.ReadView) error {
			s, err := redaction.Observe(v, blocks, repairID)
			if err != nil {
				return err
			}
			if !s.Committed || s.Materialized != materialized || !s.IdentityStable || s.BytesChanged != materialized || s.CommitHeight != 3 || s.TargetHeight != 1 {
				t.Fatalf("incorrect repair observation: %+v", s)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	checkObserved(false)
	for i := 0; i < 2; i++ {
		if err = db.View(func(v state.ReadView) error { return redaction.Materialize(v, blocks, repairID) }); err != nil {
			t.Fatal(err)
		}
		checkObserved(true)
	}
	revised := blocks.LoadBlock(1)
	if bytes.Equal(revised.Data.Txs[0], first.Data.Txs[0]) || !bytes.Equal(revised.Hash(), first.Hash()) {
		t.Fatal("history was not stably rewritten")
	}
	if !blocks.LoadBlockMeta(1).BlockID.Equals(originalID) {
		t.Fatal("full BlockID changed")
	}
	if err = vals.VerifyCommitLight(chain, originalID, 1, blocks.LoadBlockCommit(1)); err != nil {
		t.Fatal(err)
	}
	t.Run("warm-cache-after-authorized-repair", func(t *testing.T) {
		// The original child was cached before its funding was legally repaired.
		// Compare with a cache-disabled engine over the exact same current ledger.
		t.Setenv("UTXO_EXPERIMENT_DISABLE_DIRECT_CACHE", "1")
		coldEngine, err := apppkg.NewEngine(cfg, db)
		if err != nil {
			t.Fatal(err)
		}
		if err := coldEngine.EnableRepair(blocks); err != nil {
			t.Fatal(err)
		}
		for _, raw := range [][]byte{childRaw, revised.Data.Txs[0], repairRaw} {
			err := db.View(func(v state.ReadView) error {
				ctx := apppkg.BlockContext{Height: 4, Time: time.Unix(1700000034, 0)}
				warm, warmErr := warmEngine.ExecuteAt(v, raw, ctx)
				cold, coldErr := coldEngine.ExecuteAt(v, raw, ctx)
				if (warmErr == nil) != (coldErr == nil) || !reflect.DeepEqual(warm, cold) {
					t.Fatalf("repair interleaving differs: warm=%v cold=%v", warmErr, coldErr)
				}
				if len(warm.Changes) != 0 {
					t.Fatal("resubmission after repair changed ledger")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	})
	appendBlock(4, 1700000034, parentRaw)
	var late state.Creation
	err = db.View(func(v state.ReadView) error {
		late, _, err = state.Load[state.Creation](v, rules.DirectCreationKey(output, 1))
		return err
	})
	if err != nil || !late.Final {
		t.Fatal("late output missing")
	}
	body = parent.Body
	body.Inputs = []protocol.Input{{Kind: protocol.FinalInput, Output: output, Evidence: late.Fact}}
	body.Nonce[0] = 41
	body.Intent = body.IntentID()
	spendLate, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: late.Output, Instance: 1}}, pub)
	if err != nil {
		t.Fatal(err)
	}
	spendLate.Auth = []protocol.OwnerAuth{protocol.SignOwner(spendLate.ID(), f.Owner)}
	lc, err := f.DirectCertificate(spendLate, policy)
	if err != nil {
		t.Fatal(err)
	}
	lateRaw, err := (protocol.DirectPayment{Tx: spendLate, Certificate: lc}).Submission().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	appendBlock(5, 1700000035, lateRaw)
	// Start the application from genesis with an already-redacted block store.
	// Replay into the experimental backend: every historical state hash,
	// ledger row and the single debit must reproduce exactly.
	replayDB, replay, _ := newApp(true)
	defer replayDB.Close()
	for i := int64(1); i <= 5; i++ {
		original, err := blocks.LoadOriginalBlock(i)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(execute(replay, original), roots[i-1]) {
			t.Fatalf("state hash differs on replay at %d", i)
		}
		if !reflect.DeepEqual(ledgers[i-1], snapshot(replayDB)) {
			t.Fatalf("storage backend changed ledger at height %d", i)
		}
	}
	for _, storage := range []appstore.Store{db, replayDB} {
		if err = storage.View(func(v state.ReadView) error {
			balance, _, err := state.Load[uint64](v, rules.AccountKey(f.Org.Org, protocol.AssetCAL))
			if balance != 999999900 {
				t.Fatalf("wrong reserve debit %d", balance)
			}
			credit, _, err := state.Load[struct{ Credit rules.CoverageBalance }](v, state.Key(100, pc.QC.Fact[:]))
			if credit.Credit.Paid != 100 || credit.Credit.Discharged != 0 {
				t.Fatal("late parent returned spent budget")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, storage := range []appstore.Store{db, replayDB} {
		if err := storage.Close(); err != nil {
			t.Fatal("final audit export", err)
		}
	}

}
