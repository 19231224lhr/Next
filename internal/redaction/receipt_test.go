//go:build comet_v3

package redaction_test

import (
	"bytes"
	"context"
	dbm "github.com/cometbft/cometbft-db"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtstore "github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
	"os"
	"reflect"
	"testing"
	"time"
	"utxo/crypto/chameleon"
	"utxo/finality"
	apppkg "utxo/internal/committee"
	"utxo/internal/redaction"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

// receiptFixture retains real execution results and signs their successor
// headers. Its exported packages need no application database to verify.
type receiptFixture struct {
	trust    finality.Trust
	policy   rules.DirectPolicy
	db       *store.Memory
	blocks   *cmtstore.BlockStore
	engine   *apppkg.Engine
	app      *apppkg.App
	signers  []chameleon.Signer
	keys     map[string]ed25519.PrivKey
	evidence []finality.BlockData
	last     *types.Commit
	height   int64
	receipts []redaction.HistoricalReceipt
	parents  [][]byte
	TimingNS map[string]int64
}

func newReceiptFixture(t *testing.T, n, m int, batch bool) *receiptFixture {
	t.Helper()
	pub, signers, vals, keys := committee(t)
	lab := testkit.NewFixture(chain, "receipt", n)
	lab.EnableDirect()
	pemBytes, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	readerOK(t, err)
	settings, err := readerSettings(pemBytes)
	readerOK(t, err)
	policy, err := settings.Policy(lab.Schedule, []protocol.OrgConfig{lab.Org})
	readerOK(t, err)
	db := store.NewMemory()
	t.Cleanup(func() { readerOK(t, db.Close()) })
	blocks := cmtstore.NewBlockStore(dbm.NewMemDB())
	engine, err := apppkg.NewEngine(apppkg.EngineConfig{Network: lab.Org.Network, Organizations: []protocol.OrgConfig{lab.Org}, Schedule: lab.Schedule, Genesis: lab.Genesis, Direct: &settings, Accounts: []apppkg.GenesisAccount{{Owner: lab.Org.Org, Asset: protocol.AssetCAL, Balance: 1000000000}, {Owner: lab.Org.Org, Asset: protocol.AssetFUEL, Balance: 1000000000}}}, db)
	readerOK(t, err)
	readerOK(t, engine.EnableRepair(blocks))
	app, err := apppkg.NewTimedApp(chain, db, engine.Check, engine.ExecuteAt, engine.BeginBlock)
	readerOK(t, err)
	f := &receiptFixture{trust: finality.Trust{ChainID: chain, Network: lab.Org.Network, Validators: vals}, policy: policy, db: db, blocks: blocks, engine: engine, app: app, signers: signers, keys: keys, last: &types.Commit{}, TimingNS: map[string]int64{}}
	var payments [][]byte
	var missing []protocol.OutputID
	for i := 0; i < n; i++ {
		tx, err := lab.FastTransaction(i, uint64(i+1), policy)
		readerOK(t, err)
		var certs []protocol.InputCertificate
		if i < m {
			parent, err := lab.DirectCertificate(tx, policy)
			readerOK(t, err)
			raw, err := (protocol.DirectPayment{Tx: tx, Certificate: parent}).Submission().MarshalBinary()
			readerOK(t, err)
			f.parents = append(f.parents, raw)
			body := tx.Body
			body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: parent.Summary.OutputID(0), Evidence: protocol.Hash(parent.QC.Fact)}}
			body.Nonce[15] = 1
			tx, err = protocol.NewFastTx(body, []protocol.InputClaim{{Output: tx.Body.Outputs[0]}}, pub)
			readerOK(t, err)
			tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), lab.Owner)}
			certs = []protocol.InputCertificate{{Certificate: parent}}
			missing = append(missing, parent.Summary.OutputID(0))
		}
		cert, err := lab.DirectCertificate(tx, policy)
		readerOK(t, err)
		raw, err := (protocol.DirectPayment{Tx: tx, Certificate: cert, InputCertificates: certs}).Submission().MarshalBinary()
		readerOK(t, err)
		payments = append(payments, raw)
	}
	f.append(t, 1700000001, payments)
	original := receiptBody(t, f.evidence[0].Block)
	originalCommit := f.last
	f.append(t, 1700000002, [][]byte{protocol.ClockTick(lab.Org.Network, 2)})
	var decisions [][]byte
	for i, out := range missing {
		raw, err := (protocol.CompensationDecision{Network: lab.Org.Network, Output: out, Height: 1, Transaction: uint32(i), Input: 0}).MarshalBinary()
		readerOK(t, err)
		decisions = append(decisions, raw)
	}
	f.append(t, 1700000032, decisions)
	// Single commands deliberately produce later revisions of the same body.
	groups := [][]protocol.OutputID{missing}
	if !batch {
		groups = nil
		for _, out := range missing {
			groups = append(groups, []protocol.OutputID{out})
		}
	}
	for _, group := range groups {
		var raw, base, next []byte
		buildStart := time.Now()
		readerOK(t, db.View(func(v state.ReadView) error {
			_, revision, err := redaction.Canonical(v, blocks, 1)
			if err != nil {
				return err
			}
			base = bytes.Clone(revision.Body)
			var singles []protocol.RepairInput
			for _, out := range group {
				target, pay, err := redaction.InputTarget(v, blocks, policy, out, 1700000032)
				if err != nil {
					return err
				}
				var shares []chameleon.Contribution
				for _, signer := range signers[:3] {
					s, err := redaction.InputShare(v, blocks, policy, signer, out, 1700000032)
					if err != nil {
						return err
					}
					shares = append(shares, s)
				}
				target, err = redaction.ReplaceInput(policy, target, pay, shares)
				if err != nil {
					return err
				}
				singles = append(singles, target)
			}
			if batch {
				c, err := redaction.BuildBatch(v, blocks, policy, singles, 1700000032)
				if err != nil {
					return err
				}
				var rows [][]chameleon.Contribution
				for _, signer := range signers[:3] {
					s, err := redaction.BatchPartShares(v, blocks, policy, signer, c, 1700000032)
					if err != nil {
						return err
					}
					rows = append(rows, s)
				}
				c, err = redaction.CompleteBatchParts(v, blocks, policy, c, 1700000032, rows)
				if err != nil {
					return err
				}
				raw, err = c.MarshalBinary()
				return err
			}
			c := singles[0]
			var rows [][]chameleon.Contribution
			for _, signer := range signers[:3] {
				s, err := redaction.PartShares(v, blocks, policy, signer, c, 1700000032)
				if err != nil {
					return err
				}
				rows = append(rows, s)
			}
			c, err = redaction.CompleteParts(v, blocks, policy, c, 1700000032, rows)
			if err != nil {
				return err
			}
			raw, err = c.MarshalBinary()
			return err
		}))
		f.TimingNS["repair_build_adaptation"] += time.Since(buildStart).Nanoseconds()
		f.append(t, 1700000033+f.height, [][]byte{raw})
		readerOK(t, db.View(func(v state.ReadView) error {
			_, r, err := redaction.Canonical(v, blocks, 1)
			next = bytes.Clone(r.Body)
			return err
		}))
		f.receipts = append(f.receipts, redaction.HistoricalReceipt{OriginalBody: original, OriginalCommit: originalCommit, BaseBody: base, Body: next, CommandIndex: 0})
	}
	f.append(t, 1700000040, [][]byte{protocol.ClockTick(lab.Org.Network, f.height+1)})
	for i := range f.receipts {
		f.receipts[i].Execution = f.evidence[3+i]
	}
	return f
}

func (f *receiptFixture) append(t *testing.T, stamp int64, commands [][]byte, allowFailure ...bool) {
	t.Helper()
	f.height++
	txs := make(types.Txs, len(commands))
	for i, raw := range commands {
		txs[i] = raw
	}
	b := types.MakeBlock(f.height, txs, f.last, nil)
	b.ChainID = chain
	b.Time = time.Unix(stamp, 0).UTC()
	b.ValidatorsHash = f.trust.Validators.Hash()
	b.NextValidatorsHash = b.ValidatorsHash
	b.ProposerAddress = f.trust.Validators.Proposer.Address
	if f.height > 1 {
		b.LastBlockID = f.last.BlockID
		b.LastResultsHash = types.NewResults(f.evidence[len(f.evidence)-1].Results).Hash()
	}
	parts, err := b.MakePartSet(types.BlockPartSizeBytes)
	readerOK(t, err)
	f.last = commit(t, f.height, types.BlockID{Hash: b.Hash(), PartSetHeader: parts.Header()}, f.trust.Validators, f.keys)
	if len(f.evidence) > 0 {
		f.evidence[len(f.evidence)-1].Next = types.SignedHeader{Header: &b.Header, Commit: f.last}
	}
	f.blocks.SaveBlock(b, parts, f.last)
	isRepair := len(commands) > 0 && (protocol.IsRepairBatch(commands[0]) || protocol.IsRepairInput(commands[0]))
	executionStart := time.Now()
	result, err := f.app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: f.height, Hash: b.Hash(), Time: b.Time, Txs: commands})
	readerOK(t, err)
	if isRepair {
		f.TimingNS["repair_execution"] += time.Since(executionStart).Nanoseconds()
	}
	for _, r := range result.TxResults {
		if r.Code != 0 && len(allowFailure) == 0 {
			t.Fatalf("execution: %+v", r)
		}
	}
	commitStart := time.Now()
	_, err = f.app.Commit(context.Background(), &abci.RequestCommit{})
	readerOK(t, err)
	if isRepair {
		f.TimingNS["repair_commit"] += time.Since(commitStart).Nanoseconds()
	}
	f.evidence = append(f.evidence, finality.BlockData{Block: b, Results: result.TxResults})
}

func receiptBody(t *testing.T, b *types.Block) []byte {
	t.Helper()
	pb, err := b.ToProto()
	readerOK(t, err)
	raw, err := pb.Marshal()
	readerOK(t, err)
	return raw
}

func TestHistoricalReceiptRealExecution(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "batch"}[batch], func(t *testing.T) {
			f := newReceiptFixture(t, 3, 2, batch)
			for i, r := range f.receipts {
				got, err := redaction.VerifyHistoricalReceipt(f.trust, f.policy, r)
				readerOK(t, err)
				if got.TargetHeight != 1 || got.ExecutionHeight != int64(4+i) || got.Revision != uint64(i+1) || len(got.Funding) == 0 || got.Funding[0].Amount == 0 || got.Funding[0].Organization == (protocol.Hash{}) {
					t.Fatalf("bad descriptor: %+v", got)
				}
				same, err := redaction.VerifyHistoricalProjection(f.trust, f.policy, r)
				readerOK(t, err)
				if !reflect.DeepEqual(got.Funding, same.Funding) {
					t.Fatal("N*/B authentication differs")
				}
			}
			saved := f.receipts[0]
			f.append(t, 1700000041, f.parents)
			readerOK(t, f.db.Close())
			// Verification is entirely offline after later revision and repayment.
			got, err := redaction.VerifyHistoricalReceipt(f.trust, f.policy, saved)
			readerOK(t, err)
			if got.Revision != 1 {
				t.Fatal("historical version changed")
			}
		})
	}
}

func TestCompensationReceiptPrecedesRepresentation(t *testing.T) {
	f := newReceiptFixture(t, 2, 1, true)
	got, err := redaction.VerifyCompensationReceipt(f.trust, f.evidence[2], 0)
	readerOK(t, err)
	if got.ExecutionHeight != 3 || got.Decision.Height != 1 || got.Effect.Amount == 0 || got.Effect.Debit != protocol.ReserveDebitIdentity(f.trust.Network, got.Decision.Output) {
		t.Fatalf("bad economic evidence: %+v", got)
	}
}

func TestCompensationReceiptsShareBlockAuthentication(t *testing.T) {
	f := newReceiptFixture(t, 3, 2, true)
	got, err := redaction.VerifyCompensationReceipts(f.trust, f.evidence[2], []uint32{1, 0})
	readerOK(t, err)
	if len(got) != 2 {
		t.Fatalf("expected two authenticated economic effects, got %d", len(got))
	}
	for i, index := range []uint32{1, 0} {
		one, err := redaction.VerifyCompensationReceipt(f.trust, f.evidence[2], index)
		readerOK(t, err)
		if !reflect.DeepEqual(one, got[i]) {
			t.Fatal("batch economic proof differs")
		}
	}
	if _, err := redaction.VerifyCompensationReceipts(f.trust, f.evidence[2], []uint32{2}); err == nil {
		t.Fatal("bad index accepted")
	}
}

func TestLeanHistoricalAuthorization(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "batch"}[batch], func(t *testing.T) {
			f := newReceiptFixture(t, 3, 2, batch)
			for _, r := range f.receipts {
				lean := redaction.LeanHistoricalReceipt{Body: r.Body, OriginalCommit: r.OriginalCommit, Execution: r.Execution, CommandIndex: r.CommandIndex}
				full, err := redaction.VerifyHistoricalReceipt(f.trust, f.policy, r)
				readerOK(t, err)
				got, err := redaction.VerifyHistoricalAuthorization(f.trust, f.policy, lean)
				readerOK(t, err)
				if got.Command != full.Command || got.Next != full.Next || got.Revision != full.Revision || !got.OriginalBlockID.Equals(full.OriginalBlockID) || len(got.Funding) != len(full.Funding) {
					t.Fatal("lean authorization differs")
				}
				for i, row := range got.Funding {
					want := full.Funding[i]
					if row.OriginalAvailable || row.Amount != want.Amount || row.Organization != want.Organization || row.Authorized != want.Authorized || row.Decision != want.Decision {
						t.Fatal("lean funding differs")
					}
				}
				bad := lean
				bad.Body = r.BaseBody
				if _, err := redaction.VerifyHistoricalAuthorization(f.trust, f.policy, bad); err == nil {
					t.Fatal("unapproved lean body accepted")
				}
			}
		})
	}
}

func receiptCloneEvidence(t *testing.T, p finality.BlockData) finality.BlockData {
	t.Helper()
	pb, err := p.Block.ToProto()
	readerOK(t, err)
	p.Block, err = types.BlockFromProto(pb)
	readerOK(t, err)
	rs := make([]*abci.ExecTxResult, len(p.Results))
	for i, r := range p.Results {
		copy := *r
		copy.Data = bytes.Clone(r.Data)
		rs[i] = &copy
	}
	p.Results = rs
	header := *p.Next.Header
	p.Next.Header = &header
	return p
}

func (f *receiptFixture) resignResults(t *testing.T, p finality.BlockData) finality.BlockData {
	t.Helper()
	p.Next.LastResultsHash = types.NewResults(p.Results).Hash()
	p.Next.Commit = commit(t, p.Next.Height, types.BlockID{Hash: p.Next.Header.Hash(), PartSetHeader: p.Next.Commit.BlockID.PartSetHeader}, f.trust.Validators, f.keys)
	return p
}

func TestHistoricalReceiptRejectsAdversarialEvidence(t *testing.T) {
	f := newReceiptFixture(t, 3, 2, false)
	original := f.receipts[0]
	cases := map[string]func(*redaction.HistoricalReceipt){
		"raw command mutation": func(r *redaction.HistoricalReceipt) {
			r.Execution.Block.Txs[0] = bytes.Clone(r.Execution.Block.Txs[0])
			r.Execution.Block.Txs[0][10] ^= 1
		},
		"wrong slot command": func(r *redaction.HistoricalReceipt) {
			c, err := protocol.DecodeRepairInput(r.Execution.Block.Txs[0])
			readerOK(t, err)
			c.Transaction = 2
			raw, err := c.MarshalBinary()
			readerOK(t, err)
			r.Execution.Block.Txs[0] = raw
		},
		"wrong result": func(r *redaction.HistoricalReceipt) {
			r.Execution.Results[0].Data = bytes.Clone(f.receipts[1].Execution.Results[0].Data)
			r.Execution = f.resignResults(t, r.Execution)
		},
		"result tampering": func(r *redaction.HistoricalReceipt) { r.Execution.Results[0].Data[2] ^= 1 },
		"bad result decoding": func(r *redaction.HistoricalReceipt) {
			r.Execution.Results[0].Data = []byte{1}
			r.Execution = f.resignResults(t, r.Execution)
		},
		"wrong target":               func(r *redaction.HistoricalReceipt) { r.OriginalBody = receiptBody(t, f.evidence[1].Block) },
		"wrong base":                 func(r *redaction.HistoricalReceipt) { r.BaseBody = f.receipts[1].BaseBody },
		"compatible unapproved body": func(r *redaction.HistoricalReceipt) { r.Body = f.receipts[1].Body },
		"wrong command index":        func(r *redaction.HistoricalReceipt) { r.CommandIndex = 7 },
		"cached header mutation": func(r *redaction.HistoricalReceipt) {
			_ = r.Execution.Block.Hash()
			r.Execution.Block.Time = r.Execution.Block.Time.Add(time.Second)
		},
		"cached immutable tx mutation": func(r *redaction.HistoricalReceipt) {
			_ = r.Execution.Block.Hash()
			r.Execution.Block.Txs[0] = bytes.Clone(r.Execution.Block.Txs[0])
			r.Execution.Block.Txs[0][len(r.Execution.Block.Txs[0])-1] ^= 1
		},
		"wrong original mutable bytes": func(r *redaction.HistoricalReceipt) { r.OriginalBody = f.receipts[1].Body },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := original
			r.Execution = receiptCloneEvidence(t, r.Execution)
			change(&r)
			if _, err := redaction.VerifyHistoricalReceipt(f.trust, f.policy, r); err == nil {
				t.Fatal("accepted invalid evidence")
			}
			lean := redaction.LeanHistoricalReceipt{Body: r.Body, OriginalCommit: r.OriginalCommit, Execution: r.Execution, CommandIndex: r.CommandIndex}
			// Original/BaseBody exist only in audit mode, so their mutations
			// are not evidence mutations in the lean interface.
			if name != "wrong target" && name != "wrong base" && name != "wrong original mutable bytes" {
				if _, err := redaction.VerifyHistoricalAuthorization(f.trust, f.policy, lean); err == nil {
					t.Fatal("lean verifier accepted invalid evidence")
				}
			}
		})
	}
	wrong := f.trust
	wrong.Network[0] ^= 1
	if _, err := redaction.VerifyHistoricalReceipt(wrong, f.policy, original); err == nil {
		t.Fatal("wrong network accepted")
	}
	wrong = f.trust
	wrong.ChainID = "other-chain"
	if _, err := redaction.VerifyHistoricalReceipt(wrong, f.policy, original); err == nil {
		t.Fatal("wrong chain accepted")
	}
	_, _, vals, _ := committee(t)
	wrong = f.trust
	wrong.Validators = vals
	if _, err := redaction.VerifyHistoricalReceipt(wrong, f.policy, original); err == nil {
		t.Fatal("wrong committee accepted")
	}
	wrongPolicy := f.policy
	wrongPolicy.RepairCost++
	if _, err := redaction.VerifyHistoricalReceipt(f.trust, wrongPolicy, original); err == nil {
		t.Fatal("wrong rules accepted")
	}
}

func TestHistoricalReceiptRejectsRealNoopAndFailure(t *testing.T) {
	f := newReceiptFixture(t, 2, 1, true)
	saved := f.receipts[0]
	raw := saved.Execution.Block.Txs[0]
	f.append(t, 1700000041, [][]byte{raw})
	noopIndex := len(f.evidence) - 1
	bad := bytes.Clone(raw)
	bad[8] ^= 1
	f.append(t, 1700000042, [][]byte{bad}, true)
	failureIndex := len(f.evidence) - 1
	f.append(t, 1700000043, [][]byte{protocol.ClockTick(f.trust.Network, f.height+1)})
	result, err := protocol.DecodeRepairResult(f.evidence[noopIndex].Results[0].Data)
	readerOK(t, err)
	if result.Applied || f.evidence[noopIndex].Results[0].Code != 0 {
		t.Fatal("fixture is not a genuine successful noop")
	}
	for _, index := range []int{noopIndex, failureIndex} {
		r := saved
		r.Execution = f.evidence[index]
		if _, err := redaction.VerifyHistoricalReceipt(f.trust, f.policy, r); err == nil {
			t.Fatal("noop/failed command accepted as authorization")
		}
		lean := redaction.LeanHistoricalReceipt{Body: r.Body, OriginalCommit: r.OriginalCommit, Execution: r.Execution, CommandIndex: r.CommandIndex}
		if _, err := redaction.VerifyHistoricalAuthorization(f.trust, f.policy, lean); err == nil {
			t.Fatal("lean verifier accepted noop/failed command")
		}
	}
	if f.evidence[failureIndex].Results[0].Code == 0 {
		t.Fatal("fixture failure was successful")
	}
	// A cached original receipt continues to work after those later events.
	_, err = redaction.VerifyHistoricalReceipt(f.trust, f.policy, saved)
	readerOK(t, err)
}

func TestCompensationReceiptRejectsInvalidEconomicEvidence(t *testing.T) {
	f := newReceiptFixture(t, 3, 2, true)
	saved := f.evidence[2]
	// These results come from real execution: the identical decision is a
	// successful economic noop; a decision for another network fails Check.
	raw := saved.Block.Txs[0]
	f.append(t, 1700000041, [][]byte{raw})
	noopIndex := len(f.evidence) - 1
	bad := bytes.Clone(raw)
	bad[8] ^= 1
	f.append(t, 1700000042, [][]byte{bad}, true)
	failureIndex := len(f.evidence) - 1
	f.append(t, 1700000043, [][]byte{protocol.ClockTick(f.trust.Network, f.height+1)})
	noop, err := protocol.DecodeCompensationResult(f.evidence[noopIndex].Results[0].Data)
	readerOK(t, err)
	if noop.Applied || f.evidence[noopIndex].Results[0].Code != 0 || f.evidence[failureIndex].Results[0].Code == 0 {
		t.Fatal("fixture did not produce an actual economic noop and failure")
	}
	mutateResult := func(p finality.BlockData, change func(*protocol.CompensationResult)) finality.BlockData {
		result, err := protocol.DecodeCompensationResult(p.Results[0].Data)
		readerOK(t, err)
		change(&result)
		p.Results[0].Data, err = result.MarshalBinary()
		readerOK(t, err)
		return f.resignResults(t, p)
	}
	cases := map[string]struct {
		proof finality.BlockData
		index uint32
	}{
		"actual decision replay": {proof: f.evidence[noopIndex]},
		"actual failed decision": {proof: f.evidence[failureIndex]},
		"wrong index":            {proof: saved, index: 2},
	}
	wrongResult := receiptCloneEvidence(t, saved)
	wrongResult.Results[0].Data = bytes.Clone(saved.Results[1].Data)
	cases["unrelated executed result"] = struct {
		proof finality.BlockData
		index uint32
	}{proof: f.resignResults(t, wrongResult)}
	malformed := receiptCloneEvidence(t, saved)
	malformed.Results[0].Data = []byte{1}
	cases["malformed signed result"] = struct {
		proof finality.BlockData
		index uint32
	}{proof: f.resignResults(t, malformed)}
	for name, change := range map[string]func(*protocol.CompensationResult){
		"wrong decision ID": func(r *protocol.CompensationResult) { r.Decision[0] ^= 1 },
		"wrong debit":       func(r *protocol.CompensationResult) { r.Effects[0].Debit[0] ^= 1 },
		"wrong output":      func(r *protocol.CompensationResult) { r.Effects[0].Output[0] ^= 1 },
		"wrong input":       func(r *protocol.CompensationResult) { r.Effects[0].Input = 1 },
	} {
		cases[name] = struct {
			proof finality.BlockData
			index uint32
		}{proof: mutateResult(receiptCloneEvidence(t, saved), change)}
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := redaction.VerifyCompensationReceipt(f.trust, c.proof, c.index); err == nil {
				t.Fatal("single API accepted invalid economic authority")
			}
			if _, err := redaction.VerifyCompensationReceipts(f.trust, c.proof, []uint32{c.index}); err == nil {
				t.Fatal("plural API accepted invalid economic authority")
			}
		})
	}
	// An invalid selected result invalidates the plural request even if its
	// other selected result is a genuinely executed valid decision.
	if _, err := redaction.VerifyCompensationReceipts(f.trust, cases["wrong debit"].proof, []uint32{1, 0}); err == nil {
		t.Fatal("plural API returned partial authority with an invalid effect")
	}
}
