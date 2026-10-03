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
	"io"
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
	for _, repayBefore := range []bool{false, true} {
		name := "representation-before-repayment"
		if repayBefore {
			name = "repayment-before-representation"
		}
		t.Run(name, func(t *testing.T) { paymentRepairMonetaryReplay(t, repayBefore) })
	}
}

func paymentRepairMonetaryReplay(t *testing.T, repayBefore bool) {
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
	newApp := func(memory bool, retainedPath ...string) (appstore.Store, *apppkg.App, *apppkg.Engine) {
		path := filepath.Join(t.TempDir(), "committee.db")
		if len(retainedPath) != 0 {
			path = retainedPath[0]
		}
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
	// The child is already spent before its own missing source is repaired.
	// Keep a further, pre-signed payment to exercise usability after repair.
	body = child.Body
	body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: cc.Summary.OutputID(0), Evidence: protocol.Hash(cc.QC.Fact)}}
	body.Nonce[0] = 42
	body.Intent = body.IntentID()
	grandchild, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: child.Body.Outputs[0]}}, pub)
	if err != nil {
		t.Fatal(err)
	}
	grandchild.Auth = []protocol.OwnerAuth{protocol.SignOwner(grandchild.ID(), f.Owner)}
	gc, err := f.DirectCertificate(grandchild, policy)
	if err != nil {
		t.Fatal(err)
	}
	grandchildRaw, err := (protocol.DirectPayment{Tx: grandchild, Certificate: gc, InputCertificates: []protocol.InputCertificate{{Certificate: cc, Index: 0}}}).Submission().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	body = grandchild.Body
	body.Inputs = []protocol.Input{{Kind: protocol.FinalInput, Output: gc.Summary.OutputID(0), Evidence: protocol.CreationIdentity(f.Org.Network, grandchild.ID(), 0, 0)}}
	body.Nonce[0] = 43
	body.Intent = body.IntentID()
	next, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: grandchild.Body.Outputs[0]}}, pub)
	if err != nil {
		t.Fatal(err)
	}
	next.Auth = []protocol.OwnerAuth{protocol.SignOwner(next.ID(), f.Owner)}
	nc, err := f.DirectCertificate(next, policy)
	if err != nil {
		t.Fatal(err)
	}
	nextRaw, err := (protocol.DirectPayment{Tx: next, Certificate: nc}).Submission().MarshalBinary()
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
	heightOffset := int64(0)
	appendBlock := func(height, stamp int64, raw []byte) *types.Block {
		height += heightOffset
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
	appendBlock(2, 1700000002, grandchildRaw)
	grandchildBlockID := blocks.LoadBlockMeta(2).BlockID
	protectedPayments := func() []state.Entry {
		var protected []state.Entry
		for _, row := range snapshot(db) {
			for _, id := range []protocol.OutputID{cc.Summary.OutputID(0), gc.Summary.OutputID(0)} {
				if bytes.Equal(row.Key, rules.DirectCreationKey(id, 0)) || bytes.Equal(row.Key, rules.DirectSpendKey(id, 0)) {
					protected = append(protected, row)
				}
			}
		}
		return protected
	}
	beforeRepairPayments := protectedPayments()
	if len(beforeRepairPayments) != 3 {
		t.Fatalf("expected two created outputs and the consumed child, got %d records", len(beforeRepairPayments))
	}
	output := pc.Summary.OutputID(0)
	decision := protocol.CompensationDecision{Network: f.Org.Network, Output: output, Height: 1}
	decisionRaw, err := decision.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.View(func(v state.ReadView) error {
		if _, err := redaction.InputShare(v, blocks, policy, signers[0], output, 1700000032); err == nil {
			t.Fatal("share before public decision")
		}
		if _, err := redaction.ExecuteDecision(v, blocks, policy, decision, 3, 1700000030); err == nil {
			t.Fatal("premature decision accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	appendBlock(3, 1700000032, decisionRaw)
	heightOffset = 1
	parentRaw, err := (protocol.DirectPayment{Tx: parent, Certificate: pc}).Submission().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if repayBefore {
		appendBlock(3, 1700000032, parentRaw)
		heightOffset++
	}
	var command protocol.RepairInput
	var payment protocol.DirectSubmission
	var inputShares []chameleon.Contribution
	err = db.View(func(v state.ReadView) error {
		var err error
		command, payment, err = redaction.InputTarget(v, blocks, policy, output, 1700000032)
		if err != nil {
			return err
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
	for _, name := range []string{"output", "revision", "transaction", "part", "same-height", "valid-opening-wrong-reserve", "owner-auth", "organization-auth", "input-certificate"} {
		t.Run("reject-"+name, func(t *testing.T) {
			bad := command
			bad.TransactionBytes = bytes.Clone(command.TransactionBytes)
			bad.Parts = append([]chameleon.Opening(nil), command.Parts...)
			height := int64(3)
			now := int64(1700000033)
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
			case "valid-opening-wrong-reserve":
				// Model an untrusted adapter with even all fixture shares: a valid
				// opening must not authorize a different reserve debit identity.
				changed, err := protocol.DecodeDirectSubmission(bad.TransactionBytes)
				if err != nil {
					t.Fatal(err)
				}
				i := int(command.Input)
				old := payment.Tx.Funding[i]
				wrong := changed.Tx.Funding[i]
				wrong.Ref = protocol.Digest("unauthorized-reserve-debit")
				ctx := payment.Tx.FundingContext(i, policy.Key.KeyID())
				var shares []chameleon.Contribution
				for _, signer := range signers[:3] {
					share, err := signer.Adapt(ctx, old.ReferenceBytes(), wrong.ReferenceBytes(), payment.Tx.Commitments[i], old.Opening)
					if err != nil {
						t.Fatal(err)
					}
					shares = append(shares, share)
				}
				wrong.Opening, err = policy.Key.Combine(ctx, old.ReferenceBytes(), wrong.ReferenceBytes(), payment.Tx.Commitments[i], old.Opening, shares)
				if err != nil || !policy.Key.Verify(ctx, wrong.ReferenceBytes(), payment.Tx.Commitments[i], wrong.Opening) {
					t.Fatalf("must be a cryptographically valid forbidden replacement: %v", err)
				}
				changed.Tx.Funding[i] = wrong
				bad.TransactionBytes, err = changed.MarshalBinary()
				if err != nil {
					t.Fatal(err)
				}
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
				tr, err := redaction.Execute(v, blocks, policy, bad, height, now)
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
			txs := [][]byte{parentRaw, decisionRaw}
			if repairFirst {
				txs = [][]byte{decisionRaw, parentRaw}
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
					wantStatus = rules.DirectRecovered
				}
				if _, found, e := state.Load[rules.DirectRepairTodo](v, rules.DirectRepairKey(output)); e != nil || found != repairFirst {
					t.Fatalf("repair task without matching debit: found=%v err=%v", found, e)
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
	if !reflect.DeepEqual(beforeRepairPayments, protectedPayments()) {
		t.Fatal("economic repair changed descendant payment records")
	}
	beforeMaterialize := snapshot(db)
	checkObserved := func(materialized bool) {
		t.Helper()
		if err := db.View(func(v state.ReadView) error {
			s, err := redaction.Observe(v, blocks, repairID)
			if err != nil {
				return err
			}
			if !s.Committed || s.Materialized != materialized || !s.IdentityStable || s.BytesChanged != materialized || s.CommitHeight != 3+heightOffset || s.TargetHeight != 1 {
				t.Fatalf("incorrect repair observation: %+v", s)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	checkObserved(false)
	if err := db.View(func(v state.ReadView) error {
		logical, revision, err := redaction.Canonical(v, blocks, 1)
		if err != nil || revision.Number != 1 || !bytes.Equal(logical.Data.Txs[0], command.TransactionBytes) {
			t.Fatal("logical reader waited for physical installation", err)
		}
		if !bytes.Equal(blocks.LoadBlock(1).Data.Txs[0], childRaw) {
			t.Fatal("test must observe a still-original physical block")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = db.View(func(v state.ReadView) error { return redaction.Materialize(v, blocks, repairID) }); err != nil {
			t.Fatal(err)
		}
		checkObserved(true)
		if !reflect.DeepEqual(beforeMaterialize, snapshot(db)) {
			t.Fatal("materialization changed the economic ledger")
		}
	}
	revised := blocks.LoadBlock(1)
	if bytes.Equal(revised.Data.Txs[0], first.Data.Txs[0]) || !bytes.Equal(revised.Hash(), first.Hash()) {
		t.Fatal("history was not stably rewritten")
	}
	if !blocks.LoadBlockMeta(1).BlockID.Equals(originalID) {
		t.Fatal("full BlockID changed")
	}
	received := types.NewPartSetFromHeader(originalID.PartSetHeader)
	for i := 0; i < int(originalID.PartSetHeader.Total); i++ {
		if _, err = received.AddPart(blocks.LoadBlockPart(1, i)); err != nil {
			t.Fatal("stored revised part failed its original commitment", err)
		}
	}
	if !received.IsComplete() {
		t.Fatal("revised parts incomplete")
	}
	revisedProto, err := revised.ToProto()
	if err != nil {
		t.Fatal(err)
	}
	revisedWire, err := revisedProto.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	storedWire, err := io.ReadAll(received.GetReader())
	if err != nil || !bytes.Equal(storedWire, revisedWire) {
		t.Fatal("stored parts do not reconstruct the authorized revised block", err)
	}
	if err = vals.VerifyCommitLight(chain, originalID, 1, blocks.LoadBlockCommit(1)); err != nil {
		t.Fatal(err)
	}
	if !blocks.LoadBlockMeta(2).BlockID.Equals(grandchildBlockID) || !bytes.Equal(blocks.LoadBlock(2).Data.Txs[0], grandchildRaw) {
		t.Fatal("repair changed the already committed descendant")
	}
	if err = vals.VerifyCommitLight(chain, grandchildBlockID, 2, blocks.LoadBlockCommit(2)); err != nil {
		t.Fatal(err)
	}
	fixed, err := protocol.DecodeDirectSubmission(revised.Data.Txs[0])
	if err != nil {
		t.Fatal(err)
	}
	fixed.Tx.Funding = child.Funding
	fixedRaw, err := fixed.MarshalBinary()
	if err != nil || !bytes.Equal(fixedRaw, childRaw) {
		t.Fatal("repair changed fixed payment authorization or outputs", err)
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
	err = db.View(func(v state.ReadView) error {
		_, found, err := state.Load[state.Creation](v, rules.DirectCreationKey(output, 1))
		if found {
			t.Fatal("source repayment created a second user output")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	appendBlock(5, 1700000035, parentRaw) // Exact source replay cannot repay twice.
	if !reflect.DeepEqual(beforeRepairPayments, protectedPayments()) {
		t.Fatal("late source changed previously delivered descendant outputs")
	}
	// This exact request was signed before repair: no new signature, certificate,
	// ancestor history or replay of the child/grandchild is needed to spend it.
	appendBlock(6, 1700000036, nextRaw)
	if err = db.View(func(v state.ReadView) error {
		spent, found, err := state.Load[state.Spend](v, rules.DirectSpendKey(gc.Summary.OutputID(0), 0))
		if err != nil || !found || spent.Consumed != nc.QC.Fact {
			t.Fatal("pre-signed successor did not consume the original grandchild output", err)
		}
		created, found, err := state.Load[state.Creation](v, rules.DirectCreationKey(nc.Summary.OutputID(0), 0))
		if err != nil || !found || !created.Final || created.Output != next.Body.Outputs[0] {
			t.Fatal("pre-signed successor did not create its authorized output", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Start the application from genesis with an already-redacted block store.
	// Replay into the experimental backend: every historical state hash,
	// ledger row and the single debit must reproduce exactly.
	replayPath := filepath.Join(t.TempDir(), "retained-replica.db")
	replayDB, replay, _ := newApp(false, replayPath)
	defer func() { replayDB.Close() }()
	for i := int64(1); i <= int64(len(roots)); i++ {
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
		if i == 2 {
			// The lagging replica retains its committed prefix across restart.
			// Its source block store already serves revised historical bytes;
			// catch-up must still execute the original commands at every height.
			if err := replayDB.Close(); err != nil {
				t.Fatal(err)
			}
			replayDB, replay, _ = newApp(false, replayPath)
			if !reflect.DeepEqual(ledgers[i-1], snapshot(replayDB)) {
				t.Fatal("retained prefix changed across restart")
			}
		}
		if i == 1 {
			// A verified older state prefix must not borrow a later physical
			// revision, even when replay uses an already-materialized store.
			if err := replayDB.View(func(v state.ReadView) error {
				logical, revision, err := redaction.Canonical(v, blocks, 1)
				if err != nil || revision.Number != 0 || !bytes.Equal(logical.Data.Txs[0], childRaw) {
					t.Fatal("old prefix reader leaked a later physical revision", err)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, storage := range []appstore.Store{db, replayDB} {
		if err = storage.View(func(v state.ReadView) error {
			balance, _, err := state.Load[uint64](v, rules.AccountKey(f.Org.Org, protocol.AssetCAL))
			if balance != 1000000000 {
				t.Fatalf("wrong net reserve balance after repayment %d", balance)
			}
			credit, _, err := state.Load[struct{ Credit rules.CoverageBalance }](v, state.Key(100, pc.QC.Fact[:]))
			if credit.Credit.Paid != 100 || credit.Credit.Recovered != 100 || credit.Credit.Discharged != 0 {
				t.Fatal("late parent did not reconcile gross loss and recovery")
			}
			logical, revision, err := redaction.Canonical(v, blocks, 1)
			if err != nil || revision.Number != 1 || !bytes.Equal(logical.Data.Txs[0], command.TransactionBytes) {
				t.Fatal("repayment removed the authorized historical debit representation", err)
			}
			ob, found, err := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(output))
			if err != nil || !found || ob.Status != rules.DirectRecovered {
				t.Fatal("historical debit must be read separately from current repayment", err)
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
