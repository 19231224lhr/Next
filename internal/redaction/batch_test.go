//go:build comet_v3

package redaction_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	dbm "github.com/cometbft/cometbft-db"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtstore "github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"
	"utxo/crypto/chameleon"
	apppkg "utxo/internal/committee"
	"utxo/internal/redaction"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestAtomicRepairBatchTwoInputs(t *testing.T) { testAtomicRepairBatch(t, 2) }
func TestRepairBatchCost(t *testing.T) {
	for _, n := range []int{1, 4, 8} {
		t.Run(fmt.Sprintf("same_block_%d", n), func(t *testing.T) { testAtomicRepairBatch(t, n) })
	}
	t.Run("separate_blocks_8", func(t *testing.T) {
		for i := 0; i < 8; i++ {
			testAtomicRepairBatch(t, 1)
		}
	})
}
func testAtomicRepairBatch(t *testing.T, count int) {
	pub, signers, vals, keys := committee(t)
	f := testkit.NewFixture(chain, "batch-org", count)
	f.EnableDirect()
	if count == 2 {
		f.Genesis.Outputs[0].Output.Amount = 40
		f.Genesis.Outputs[1].Output.Amount = 60
	}
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	blockKey, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(blockKey.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	settings := rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	policy, err := settings.Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if err != nil {
		t.Fatal(err)
	}
	cfg := apppkg.EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Direct: &settings, Accounts: []apppkg.GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetCAL, Balance: 1000000000}, {Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1000000000}}}
	blocks := cmtstore.NewBlockStore(dbm.NewMemDB())
	db := store.NewMemory()
	defer db.Close()
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
	var parents []protocol.OutputCertificate
	var inputs []protocol.Input
	var claims []protocol.InputClaim
	var certs []protocol.InputCertificate
	var body protocol.TxBody
	for i := 0; i < count; i++ {
		tx, e := f.FastTransaction(i, 1, policy)
		if e != nil {
			t.Fatal(e)
		}
		cert, e := f.DirectCertificate(tx, policy)
		if e != nil {
			t.Fatal(e)
		}
		parents = append(parents, cert)
		inputs = append(inputs, protocol.Input{Kind: protocol.CertificateInput, Output: cert.Summary.OutputID(0), Evidence: protocol.Hash(cert.QC.Fact)})
		claims = append(claims, protocol.InputClaim{Output: tx.Body.Outputs[0]})
		certs = append(certs, protocol.InputCertificate{Certificate: cert, Index: 0})
		body = tx.Body
	}
	order := make([]int, len(inputs))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return bytes.Compare(inputs[order[i]].Output[:], inputs[order[j]].Output[:]) < 0 })
	sortedInputs := make([]protocol.Input, len(inputs))
	sortedClaims := make([]protocol.InputClaim, len(inputs))
	sortedCerts := make([]protocol.InputCertificate, len(inputs))
	for i, j := range order {
		sortedInputs[i] = inputs[j]
		sortedClaims[i] = claims[j]
		sortedCerts[i] = certs[j]
	}
	inputs, claims, certs = sortedInputs, sortedClaims, sortedCerts
	body.Inputs = inputs
	body.Outputs = append([]protocol.Output(nil), body.Outputs...)
	body.Outputs[0].Amount = 0
	for _, c := range claims {
		body.Outputs[0].Amount += c.Output.Amount
	}
	body.Nonce[0] = 40
	body.Work.Ancestors = 1
	body.Intent = body.IntentID()
	tx, err := protocol.NewFastTx(body, claims, pub)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.Owner)}
	cert, err := f.DirectCertificate(tx, policy)
	if err != nil {
		t.Fatal(err)
	}
	paymentRaw, err := (protocol.DirectPayment{Tx: tx, Certificate: cert, InputCertificates: certs}).Submission().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var roots [][]byte
	last := &types.Commit{}
	appendBlock := func(height, stamp int64, raw []byte) []byte {
		b := block(t, height, raw, last, vals)
		b.Time = time.Unix(stamp, 0).UTC()
		parts, e := b.MakePartSet(types.BlockPartSizeBytes)
		if e != nil {
			t.Fatal(e)
		}
		id := types.BlockID{Hash: b.Hash(), PartSetHeader: parts.Header()}
		last = commit(t, height, id, vals, keys)
		blocks.SaveBlock(b, parts, last)
		r, e := app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: height, Hash: b.Hash(), Time: b.Time, Txs: [][]byte{raw}})
		if e != nil {
			t.Fatal(e)
		}
		if r.TxResults[0].Code != 0 {
			t.Fatalf("execution: %+v", r.TxResults[0])
		}
		if _, e = app.Commit(context.Background(), &abci.RequestCommit{}); e != nil {
			t.Fatal(e)
		}
		roots = append(roots, bytes.Clone(r.AppHash))
		return r.TxResults[0].Data
	}
	appendBlock(1, 1700000001, paymentRaw)
	originalID := blocks.LoadBlockMeta(1).BlockID
	// The recipient spends the merged output before either source is repaired.
	successorBody := tx.Body
	successorBody.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: cert.Summary.OutputID(0), Evidence: protocol.Hash(cert.QC.Fact)}}
	successorBody.Nonce[0] = 41
	successorBody.Intent = successorBody.IntentID()
	successor, e := protocol.NewFastTx(successorBody, []protocol.InputClaim{{Output: tx.Body.Outputs[0]}}, pub)
	if e != nil {
		t.Fatal(e)
	}
	successor.Auth = []protocol.OwnerAuth{protocol.SignOwner(successor.ID(), f.Owner)}
	successorCert, e := f.DirectCertificate(successor, policy)
	if e != nil {
		t.Fatal(e)
	}
	successorRaw, e := (protocol.DirectPayment{Tx: successor, Certificate: successorCert, InputCertificates: []protocol.InputCertificate{{Certificate: cert, Index: 0}}}).Submission().MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	appendBlock(2, 1700000002, successorRaw)
	successorID := blocks.LoadBlockMeta(2).BlockID
	var c protocol.RepairBatch
	batchStarted := time.Now()
	err = db.View(func(v state.ReadView) error {
		var singles []protocol.RepairInput
		for _, parent := range parents {
			target, pay, e := redaction.InputTarget(v, blocks, policy, parent.Summary.OutputID(0), 1700000032)
			if e != nil {
				return e
			}
			var shares []chameleon.Contribution
			for _, signer := range signers[:3] {
				s, e := redaction.InputShare(v, blocks, policy, signer, parent.Summary.OutputID(0), 1700000032)
				if e != nil {
					return e
				}
				shares = append(shares, s)
			}
			target, e = redaction.ReplaceInput(policy, target, pay, shares)
			if e != nil {
				return e
			}
			singles = append(singles, target)
		}
		var e error
		c, e = redaction.BuildBatch(v, blocks, policy, singles, 1700000032)
		if e != nil {
			return e
		}
		var votes [][]chameleon.Contribution
		for _, signer := range signers[:3] {
			s, e := redaction.BatchPartShares(v, blocks, policy, signer, c, 1700000032)
			if e != nil {
				return e
			}
			votes = append(votes, s)
		}
		c, e = redaction.CompleteBatchParts(v, blocks, policy, c, 1700000032, votes)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	batchBuildDuration := time.Since(batchStarted)
	raw, err = c.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	changed := bytes.Clone(raw)
	changed[len(changed)-1] ^= 1
	if bytes.Equal(types.Tx(raw).Hash(), types.Tx(changed).Hash()) {
		t.Fatal("batch proof excluded from consensus identity")
	}
	err = db.View(func(v state.ReadView) error {
		limited := state.NewOverlay(v)
		if e := state.Put(limited, rules.AccountKey(f.Org.Org, protocol.AssetCAL), body.Outputs[0].Amount-1); e != nil {
			return e
		}
		tr, e := redaction.ExecuteBatch(limited, blocks, policy, c, 3, 1700000032)
		if e == nil || len(tr.Changes) != 0 || len(tr.Data) != 0 {
			t.Fatal("partially affordable batch leaked effects")
		}
		for _, parent := range parents {
			ob, _, e := state.Load[rules.DirectObligation](limited, rules.DirectObligationKey(parent.Summary.OutputID(0)))
			if e != nil {
				return e
			}
			if ob.Status != rules.DirectOpen {
				t.Fatal("failed batch mutated caller view")
			}
		}
		closed := state.NewOverlay(v)
		ob, _, e := state.Load[rules.DirectObligation](closed, rules.DirectObligationKey(parents[0].Summary.OutputID(0)))
		if e != nil {
			return e
		}
		ob.Status = rules.DirectFulfilled
		if e = state.Put(closed, rules.DirectObligationKey(ob.Output), ob); e != nil {
			return e
		}
		if tr, e := redaction.ExecuteBatch(closed, blocks, policy, c, 3, 1700000032); e == nil || len(tr.Changes) != 0 {
			t.Fatal("closed source accepted")
		}
		stale := c
		stale.Base++
		if _, e := redaction.ExecuteBatch(v, blocks, policy, stale, 3, 1700000032); e == nil {
			t.Fatal("stale base accepted")
		}
		forged := c
		forged.Parts = append([]chameleon.Opening(nil), c.Parts...)
		forged.Parts[0][0] ^= 1
		if _, e := redaction.ExecuteBatch(v, blocks, policy, forged, 3, 1700000032); e == nil {
			t.Fatal("invalid block witness accepted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.View(func(v state.ReadView) error {
		sequential := state.NewOverlay(v)
		var legacy []protocol.RepairInput
		sequentialBytes, sequentialParts, batchParts := 0, 0, 0
		started := time.Now()
		for _, parent := range parents {
			target, pay, e := redaction.InputTarget(sequential, blocks, policy, parent.Summary.OutputID(0), 1700000032)
			if e != nil {
				return e
			}
			var shares []chameleon.Contribution
			for _, signer := range signers[:3] {
				share, e := redaction.InputShare(sequential, blocks, policy, signer, parent.Summary.OutputID(0), 1700000032)
				if e != nil {
					return e
				}
				shares = append(shares, share)
			}
			target, e = redaction.ReplaceInput(policy, target, pay, shares)
			if e != nil {
				return e
			}
			requests, _, _, e := redaction.PartRequests(sequential, blocks, policy, target, 1700000032)
			if e != nil {
				return e
			}
			sequentialParts += len(requests)
			var votes [][]chameleon.Contribution
			for _, signer := range signers[:3] {
				row, e := redaction.PartShares(sequential, blocks, policy, signer, target, 1700000032)
				if e != nil {
					return e
				}
				votes = append(votes, row)
			}
			target, e = redaction.CompleteParts(sequential, blocks, policy, target, 1700000032, votes)
			if e != nil {
				return e
			}
			wire, e := target.MarshalBinary()
			if e != nil {
				return e
			}
			sequentialBytes += len(wire)
			tr, e := redaction.Execute(sequential, blocks, policy, target, 3, 1700000032)
			if e != nil {
				return e
			}
			sequential.Apply(tr.Changes)
			legacy = append(legacy, target)
		}
		sequentialDuration := time.Since(started)
		requests, _, _, e := redaction.BatchPartRequests(v, blocks, policy, c, 1700000032)
		if e != nil {
			return e
		}
		batchParts = len(requests)
		started = time.Now()
		tr, e := redaction.ExecuteBatch(v, blocks, policy, c, 3, 1700000032)
		if e != nil {
			return e
		}
		batchDuration := batchBuildDuration + time.Since(started)
		economic := func(changes []state.Change) []state.Change {
			var out []state.Change
			for _, change := range changes {
				kind := change.Key[2]
				if kind < 107 || kind == 111 || kind == 112 {
					out = append(out, change)
				}
			}
			return out
		}
		if !reflect.DeepEqual(economic(sequential.Changes()), economic(tr.Changes)) {
			t.Fatal("batch differs from sequential economic state")
		}
		// Cross-entry repeats and partially overlapping batches cannot debit again.
		applied := state.NewOverlay(v)
		applied.Apply(tr.Changes)
		if again, e := redaction.ExecuteBatch(sequential, blocks, policy, c, 3, 1700000032); e == nil || len(again.Changes) != 0 {
			t.Fatal("batch after legacy debited again")
		}
		for _, single := range legacy {
			if again, _ := redaction.Execute(applied, blocks, policy, single, 3, 1700000032); len(again.Changes) != 0 {
				t.Fatal("legacy after batch debited again")
			}
		}
		if _, e = redaction.BuildBatch(applied, blocks, policy, []protocol.RepairInput{{Output: parents[0].Summary.OutputID(0)}}, 1700000032); e == nil {
			t.Fatal("closed item regrouped")
		}
		t.Logf("COST items=%d single_commands=%d batch_commands=1 single_bytes=%d batch_bytes=%d single_part_adaptations=%d batch_part_adaptations=%d single_ms=%.3f batch_ms=%.3f", count, count, sequentialBytes, len(raw), sequentialParts, batchParts, float64(sequentialDuration.Microseconds())/1000, float64(batchDuration.Microseconds())/1000)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	resultRaw := appendBlock(3, 1700000032, raw)
	result, err := protocol.DecodeRepairBatchResult(resultRaw)
	if err != nil || !result.Applied || len(result.Effects) != count {
		t.Fatal("batch result", err)
	}
	err = db.View(func(v state.ReadView) error {
		balance, _, e := state.Load[uint64](v, rules.AccountKey(f.Org.Org, protocol.AssetCAL))
		if e != nil {
			return e
		}
		if balance != 1000000000-body.Outputs[0].Amount {
			t.Fatalf("wrong debit %d", balance)
		}
		for _, parent := range parents {
			ob, _, e := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(parent.Summary.OutputID(0)))
			if e != nil {
				return e
			}
			if ob.Status != rules.DirectRepaired {
				t.Fatal("unclosed obligation")
			}
		}
		return redaction.Materialize(v, blocks, c.ID())
	})
	if err != nil {
		t.Fatal(err)
	}
	if !blocks.LoadBlockMeta(2).BlockID.Equals(successorID) || !bytes.Equal(blocks.LoadBlock(2).Data.Txs[0], successorRaw) {
		t.Fatal("repair propagated into successor")
	}
	if !blocks.LoadBlockMeta(1).BlockID.Equals(originalID) {
		t.Fatal("block identity changed")
	}
	repaired, err := protocol.DecodeDirectSubmission(blocks.LoadBlock(1).Data.Txs[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, funding := range repaired.Tx.Funding {
		if funding.Kind != protocol.ReserveFunding {
			t.Fatal("one patch overwrote another")
		}
	}
	repaired.Tx.Funding = tx.Funding
	fixed, err := repaired.MarshalBinary()
	if err != nil || !bytes.Equal(fixed, paymentRaw) {
		t.Fatal("fixed authorization changed", err)
	}
	repeated := appendBlock(4, 1700000033, raw)
	again, err := protocol.DecodeRepairBatchResult(repeated)
	if err != nil || again.Applied || len(again.Effects) != 0 {
		t.Fatal("repeat economic effects", err)
	}
	replayDB := store.NewMemory()
	defer replayDB.Close()
	replayEngine, err := apppkg.NewEngine(cfg, replayDB)
	if err != nil {
		t.Fatal(err)
	}
	if err = replayEngine.EnableRepair(blocks); err != nil {
		t.Fatal(err)
	}
	replayApp, err := apppkg.NewTimedApp(chain, replayDB, replayEngine.Check, replayEngine.ExecuteAt, replayEngine.BeginBlock)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range roots {
		b, e := blocks.LoadOriginalBlock(int64(i + 1))
		if e != nil {
			t.Fatal(e)
		}
		txs := make([][]byte, len(b.Data.Txs))
		for i, x := range b.Data.Txs {
			txs[i] = x
		}
		r, e := replayApp.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: b.Height, Time: b.Time, Hash: b.Hash(), Txs: txs})
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(r.AppHash, want) {
			t.Fatalf("replay differs at height %d", b.Height)
		}
		if _, e = replayApp.Commit(context.Background(), &abci.RequestCommit{}); e != nil {
			t.Fatal(e)
		}
	}

}
