//go:build comet_v3

package redaction_test

// This experiment is an internal, executed-prefix reader, not a remote proof
// protocol. All setup commands pass the production ABCI state machine.
import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtstore "github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
	"utxo/crypto/chameleon"
	apppkg "utxo/internal/committee"
	"utxo/internal/redaction"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

type readerIO struct{ Gets, Scans, Bytes int64 }
type readerView struct {
	state.ReadView
	io *readerIO
}

func (v readerView) Get(k []byte) ([]byte, error) {
	b, e := v.ReadView.Get(k)
	v.io.Gets++
	v.io.Bytes += int64(len(b))
	return b, e
}
func (v readerView) Scan(p, a []byte, n int) ([]state.Entry, error) {
	es, e := v.ReadView.(state.ScanView).Scan(p, a, n)
	v.io.Scans++
	for _, x := range es {
		v.io.Bytes += int64(len(x.Key) + len(x.Value))
	}
	return es, e
}

type readerDB struct {
	dbm.DB
	io readerIO
}

func (d *readerDB) Get(k []byte) ([]byte, error) {
	b, e := d.DB.Get(k)
	d.io.Gets++
	d.io.Bytes += int64(len(b))
	return b, e
}

type readerAnswer struct {
	Prefix, Height int64
	Transaction    uint32
	Output         protocol.OutputID
	Tx             protocol.TxID
	Input          uint32
	Amount         uint64
	Kind           uint8
	Ref            protocol.Hash
	Status         uint8
	Decision       protocol.Hash
	DecisionHeight int64
}
type readerSample struct {
	ReadNS, ResolveNS, TotalNS int64
	App, Block                 readerIO
}
type readerCase struct {
	Workload, Method   string
	Samples            []readerSample
	AllocBytes, Allocs float64
	CryptoNS           []int64
	InputCHNS          []int64
}
type readerReport struct {
	Payments, Repairs int
	Prefix            int64
	Build             string
	TimingNS          map[string]int64
	Storage           map[string]int64
	Cases             []readerCase
	Checks            []string
	IdentityNS        map[string][]int64
}
type readerFixture struct {
	db                    *store.Bolt
	validators            *types.ValidatorSet
	original, revised     *cmtstore.BlockStore
	originalDB, revisedDB *readerDB
	policy                rules.DirectPolicy
	height                int64
	count, repaired       int
	path                  string
	report                readerReport
	late                  func()
	batch                 protocol.RepairBatch
}

func readerOK(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}

func readerSettings(raw []byte) (rules.DirectSettings, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return rules.DirectSettings{}, protocol.ErrEncoding
	}
	key, e := x509.ParsePKCS1PrivateKey(block.Bytes)
	if e != nil {
		return rules.DirectSettings{}, e
	}
	return rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}, nil
}

func readerNegativeChecks(t *testing.T, f *readerFixture) {
	readerOK(t, f.db.View(func(v state.ReadView) error {
		old, e := f.original.LoadOriginalBlock(1)
		if e != nil {
			return e
		}
		newer, _, e := redaction.Canonical(v, f.revised, 1)
		if e != nil {
			return e
		}
		a, e := protocol.DecodeDirectSubmission(old.Data.Txs[0])
		if e != nil {
			return e
		}
		b, e := protocol.DecodeDirectSubmission(newer.Data.Txs[0])
		if e != nil {
			return e
		}
		out := a.Tx.Body.Inputs[0].Output
		todo, ok, e := state.Load[rules.DirectRepairTodo](v, rules.DirectRepairKey(out))
		if e != nil || !ok {
			return fmt.Errorf("missing fixture decision: %v", e)
		}
		for _, pay := range []protocol.DirectSubmission{a, b} {
			revised := pay.Tx.Funding[0].Kind == protocol.ReserveFunding
			for _, test := range []string{"coordinate", "prefix", "debit", "amount"} {
				o := state.NewOverlay(v)
				j := 0
				h := f.height
				bad := todo
				switch test {
				case "coordinate":
					j = 1
				case "prefix":
					h = 2
				case "debit":
					bad.Debit[0] ^= 1
					readerOK(t, state.Put(o, rules.DirectRepairKey(out), bad))
				case "amount":
					bad.Obligation.Amount++
					readerOK(t, state.Put(o, rules.DirectRepairKey(out), bad))
				}
				if _, e := readerResolve(o, f.policy, pay, 1, j, h, revised, nil); e == nil {
					t.Fatal("accepted", test, revised)
				}
			}
		}
		o := state.NewOverlay(v)
		o.Delete(rules.DirectRepairKey(out))
		if _, e := readerResolve(o, f.policy, b, 1, 0, f.height, true, nil); e == nil {
			t.Fatal("reserve representation without decision")
		}
		bad := b
		bad.Tx.Funding = append([]protocol.Funding(nil), b.Tx.Funding...)
		bad.Tx.Funding[0].Ref[0] ^= 1
		if _, e := readerResolve(v, f.policy, bad, 1, 0, f.height, true, nil); e == nil {
			t.Fatal("wrong representation debit")
		}
		o = state.NewOverlay(v)
		readerOK(t, state.Put(o, redaction.RevisionKey(1), redaction.Revision{Number: 1, Body: []byte{255}}))
		if _, _, e := redaction.Canonical(o, f.revised, 1); e == nil {
			t.Fatal("corrupt revision fell back")
		}
		readerOK(t, readerCrypto(a, f.policy))
		readerOK(t, readerCrypto(b, f.policy))
		for _, pay := range []protocol.DirectSubmission{a, b} {
			pay.Authorization.Votes = append([]protocol.SpendVote(nil), pay.Authorization.Votes...)
			pay.Authorization.Votes[0].Signature[0] ^= 1
			if readerCrypto(pay, f.policy) == nil {
				t.Fatal("bad organization signature accepted")
			}
		}
		return nil
	}))
	f.report.Checks = append(f.report.Checks, "wrong coordinate/prefix/debit/amount rejected by both", "unbacked reserve and corrupt revision rejected", "both valid crypto pass; changed signature rejected")
}

// Both readers return effective economic provenance; physical representation
// and revision are deliberately not equated with current obligation status.
func readerResolve(v state.ReadView, p rules.DirectPolicy, pay protocol.DirectSubmission,
	h int64, j int, prefix int64, representation bool, indexed map[protocol.OutputID]bool) ([]readerAnswer, error) {
	out := make([]readerAnswer, 0, len(pay.Tx.Body.Inputs))
	for k, in := range pay.Tx.Body.Inputs {
		f := pay.Tx.Funding[k]
		a := readerAnswer{Prefix: prefix, Height: h, Transaction: uint32(j), Output: in.Output, Tx: pay.Tx.ID(), Input: uint32(k), Amount: pay.Tx.Claims[k].Output.Amount, Kind: protocol.OriginalFunding, Ref: protocol.Hash(in.Output)}
		if !representation && (f.Kind != protocol.OriginalFunding || f.Ref != a.Ref) {
			return nil, protocol.ErrAuth
		}
		var todo rules.DirectRepairTodo
		var found bool
		var err error
		if in.Kind == protocol.CertificateInput && (indexed == nil || indexed[in.Output]) {
			todo, found, err = state.Load[rules.DirectRepairTodo](v, rules.DirectRepairKey(in.Output))
			if err != nil {
				return nil, err
			}
		}
		if found {
			c, ob := todo.Decision, todo.Obligation
			if todo.DecisionHeight <= 0 || todo.DecisionHeight > prefix || c.Network != pay.Tx.Body.Network || c.Height != h || int(c.Transaction) != j || int(c.Input) != k || c.Output != in.Output || ob.Output != in.Output || ob.Transaction != a.Tx || int(ob.Input) != k || ob.Amount != a.Amount || todo.Debit != protocol.ReserveDebitIdentity(c.Network, c.Output) {
				return nil, protocol.ErrAuth
			}
			cfg, ok := p.Organizations[ob.Config]
			if !ok || cfg.Org != ob.Issuer {
				return nil, protocol.ErrAuth
			}
			a.Kind, a.Ref, a.Decision, a.DecisionHeight = protocol.ReserveFunding, todo.Debit, c.ID(), todo.DecisionHeight
		} else if indexed != nil && indexed[in.Output] {
			return nil, protocol.ErrAuth
		}
		if in.Kind == protocol.CertificateInput {
			ob, ok, e := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(in.Output))
			if e != nil {
				return nil, e
			}
			if !ok {
				return nil, rules.ErrMissing
			}
			a.Status = ob.Status
		}
		if representation {
			// A decision may precede representation: original means "not represented
			// yet", not "not paid". A reserve reference must match the public debit.
			if f.Kind == protocol.ReserveFunding {
				if !found || f.Ref != a.Ref {
					return nil, protocol.ErrAuth
				}
			} else if f.Kind != protocol.OriginalFunding || f.Ref != protocol.Hash(in.Output) {
				return nil, protocol.ErrAuth
			}
		}
		out = append(out, a)
	}
	return out, nil
}

func (f *readerFixture) read(method, work string) (answers []readerAnswer, sample readerSample, err error) {
	started := time.Now()
	appIO := readerIO{}
	bd := f.originalDB
	bs := f.original
	revised := method == "canonical"
	if revised {
		bd = f.revisedDB
		bs = f.revised
	}
	bd.io = readerIO{}
	err = f.db.View(func(base state.ReadView) error {
		v := readerView{base, &appIO}
		var meta struct{ Height int64 }
		raw, e := v.Get([]byte{0xff, 2, 'c', 'o', 'm', 'm', 'i', 't'})
		if e != nil {
			return e
		}
		if e = json.Unmarshal(raw, &meta); e != nil {
			return e
		}
		if meta.Height != f.height || meta.Height < 1 {
			return protocol.ErrAuth
		}
		var b *types.Block
		if revised {
			b, _, e = redaction.Canonical(v, bs, 1)
		} else {
			b, e = bs.LoadOriginalBlock(1)
		}
		if e != nil {
			return e
		}
		if b == nil {
			return rules.ErrMissing
		}
		indices := []int{0}
		if work == "point_normal" {
			indices[0] = f.count - 1
		}
		if work == "block" {
			indices = make([]int, len(b.Data.Txs))
			for i := range indices {
				indices[i] = i
			}
		}
		pays := make([]protocol.DirectSubmission, len(indices))
		for i, j := range indices {
			p, e := protocol.DecodeDirectSubmission(b.Data.Txs[j])
			if e != nil {
				return e
			}
			pays[i] = p
		}
		sample.ReadNS = time.Since(started).Nanoseconds()
		resolve := time.Now()
		var index map[protocol.OutputID]bool
		if !revised && work == "block" {
			index = make(map[protocol.OutputID]bool)
			key := redaction.DecisionIndexKey(1, protocol.OutputID{})
			prefix := key[:len(key)-32]
			entries, e := v.Scan(prefix, nil, 1024)
			if e != nil {
				return e
			}
			for _, x := range entries {
				var ob rules.DirectObligation
				if e = json.Unmarshal(x.Value, &ob); e != nil {
					return e
				}
				index[ob.Output] = true
			}
		}
		for i, pay := range pays {
			a, e := readerResolve(v, f.policy, pay, 1, indices[i], meta.Height, revised, index)
			if e != nil {
				return e
			}
			answers = append(answers, a...)
		}
		sample.ResolveNS = time.Since(resolve).Nanoseconds()
		return nil
	})
	sample.TotalNS = time.Since(started).Nanoseconds()
	sample.App, sample.Block = appIO, bd.io
	return
}

// Optional rechecking uses identical signature/certificate checks on each
// representation and its own CH openings. It is not a substitute for the
// successful public decision/representation execution used by both readers.
func readerCrypto(pay protocol.DirectSubmission, p rules.DirectPolicy) error {
	if e := pay.Tx.VerifyAuth(); e != nil {
		return e
	}
	cfg, ok := p.Organizations[pay.Tx.Body.Config]
	if !ok {
		return protocol.ErrAuth
	}
	if pay.Authorization.Fact != pay.Summary().Fact() {
		return protocol.ErrAuth
	}
	if e := protocol.VerifyQC(pay.Authorization, cfg); e != nil {
		return e
	}
	for _, c := range pay.InputCertificates {
		cfg, ok := p.Organizations[c.Certificate.Summary.Config]
		if !ok {
			return protocol.ErrAuth
		}
		if e := c.Certificate.Verify(cfg); e != nil {
			return e
		}
	}
	return nil
}
func readerInputCH(pay protocol.DirectSubmission, p rules.DirectPolicy) error {
	for i, f := range pay.Tx.Funding {
		if !p.Key.Verify(pay.Tx.FundingContext(i, p.Key.KeyID()), f.ReferenceBytes(), pay.Tx.Commitments[i], f.Opening) {
			return protocol.ErrAuth
		}
	}
	return nil
}

func newReaderFixture(t *testing.T, path string, n, m int) *readerFixture {
	t.Helper()
	readerOK(t, os.MkdirAll(path, 0700))
	pub, signers, vals, keys := committee(t)
	lab := testkit.NewFixture(chain, "reader-cost", n)
	lab.EnableDirect()
	// Reuse the already configured production modulus, without reading or
	// exporting any experiment-private key into result artifacts.
	keyBytes, e := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	readerOK(t, e)
	settings, e := readerSettings(keyBytes)
	readerOK(t, e)
	policy, e := settings.Policy(lab.Schedule, []protocol.OrgConfig{lab.Org})
	readerOK(t, e)
	db, e := store.Open(filepath.Join(path, "application.db"), store.Identity{Network: chain, Role: "reader-test", Node: "replica", Schema: 1})
	readerOK(t, e)
	t.Cleanup(func() { readerOK(t, db.Close()) })
	od, e := dbm.NewDB("original", dbm.GoLevelDBBackend, path)
	readerOK(t, e)
	rd, e := dbm.NewDB("revised", dbm.GoLevelDBBackend, path)
	readerOK(t, e)
	t.Cleanup(func() { readerOK(t, od.Close()); readerOK(t, rd.Close()) })
	f := &readerFixture{db: db, originalDB: &readerDB{DB: od}, revisedDB: &readerDB{DB: rd}, policy: policy, count: n, repaired: m, path: path, report: readerReport{Payments: n, Repairs: m, Build: "721800c-production", TimingNS: map[string]int64{}, Storage: map[string]int64{}}}
	f.validators = vals
	f.original = cmtstore.NewBlockStore(f.originalDB)
	f.revised = cmtstore.NewBlockStore(f.revisedDB)
	engine, e := apppkg.NewEngine(apppkg.EngineConfig{Network: lab.Org.Network, Organizations: []protocol.OrgConfig{lab.Org}, Schedule: lab.Schedule, Genesis: lab.Genesis, Direct: &settings, Accounts: []apppkg.GenesisAccount{{Owner: lab.Org.Org, Asset: protocol.AssetCAL, Balance: 1000000000}, {Owner: lab.Org.Org, Asset: protocol.AssetFUEL, Balance: 1000000000}}}, db)
	readerOK(t, e)
	readerOK(t, engine.EnableRepair(f.revised))
	app, e := apppkg.NewTimedApp(chain, db, engine.Check, engine.ExecuteAt, engine.BeginBlock)
	readerOK(t, e)
	last := &types.Commit{}
	appendBlock := func(stamp int64, commands [][]byte, stage string, copyOriginal bool) {
		f.height++
		txs := make([]types.Tx, len(commands))
		for i, b := range commands {
			readerOK(t, engine.Check(b))
			txs[i] = b
		}
		b := types.MakeBlock(f.height, txs, last, nil)
		b.ChainID = chain
		b.Time = time.Unix(stamp, 0).UTC()
		b.ValidatorsHash = vals.Hash()
		b.NextValidatorsHash = vals.Hash()
		b.ProposerAddress = vals.Proposer.Address
		if f.height > 1 {
			b.LastBlockID = last.BlockID
		}
		readerOK(t, b.ValidateBasic())
		parts, e := b.MakePartSet(types.BlockPartSizeBytes)
		readerOK(t, e)
		last = commit(t, f.height, types.BlockID{Hash: b.Hash(), PartSetHeader: parts.Header()}, vals, keys)
		f.revised.SaveBlock(b, parts, last)
		if copyOriginal {
			f.original.SaveBlock(b, parts, last)
		}
		start := time.Now()
		r, e := app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: f.height, Hash: b.Hash(), Time: b.Time, Txs: commands})
		readerOK(t, e)
		f.report.TimingNS[stage+"_finalize"] = time.Since(start).Nanoseconds()
		for i, r := range r.TxResults {
			if r.Code != 0 {
				t.Fatalf("%s command %d: %+v", stage, i, r)
			}
		}
		start = time.Now()
		_, e = app.Commit(context.Background(), &abci.RequestCommit{})
		readerOK(t, e)
		f.report.TimingNS[stage+"_commit"] = time.Since(start).Nanoseconds()
	}
	var commands, parentCommands [][]byte
	var missing []protocol.OutputID
	for i := 0; i < n; i++ {
		tx, e := lab.FastTransaction(i, uint64(i+1), policy)
		readerOK(t, e)
		var parents []protocol.InputCertificate
		if i < m {
			pc, e := lab.DirectCertificate(tx, policy)
			readerOK(t, e)
			parentRaw, e := (protocol.DirectPayment{Tx: tx, Certificate: pc}).Submission().MarshalBinary()
			readerOK(t, e)
			parentCommands = append(parentCommands, parentRaw)
			body := tx.Body
			body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: pc.Summary.OutputID(0), Evidence: protocol.Hash(pc.QC.Fact)}}
			body.Nonce[15] = 1
			body.Intent = body.IntentID()
			tx, e = protocol.NewFastTx(body, []protocol.InputClaim{{Output: tx.Body.Outputs[0]}}, pub)
			readerOK(t, e)
			tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), lab.Owner)}
			parents = []protocol.InputCertificate{{Certificate: pc}}
			missing = append(missing, pc.Summary.OutputID(0))
		}
		cert, e := lab.DirectCertificate(tx, policy)
		readerOK(t, e)
		raw, e := (protocol.DirectPayment{Tx: tx, Certificate: cert, InputCertificates: parents}).Submission().MarshalBinary()
		readerOK(t, e)
		commands = append(commands, raw)
	}
	appendBlock(1700000001, commands, "payments", true)
	appendBlock(1700000002, [][]byte{protocol.ClockTick(lab.Org.Network, 2)}, "tick", true)
	var decisions [][]byte
	for i, out := range missing {
		b, e := (protocol.CompensationDecision{Network: lab.Org.Network, Output: out, Height: 1, Transaction: uint32(i), Input: 0}).MarshalBinary()
		readerOK(t, e)
		decisions = append(decisions, b)
	}
	appendBlock(1700000032, decisions, "decisions", true)
	readerSnapshot(t, f, "before")
	// Pre-representation equivalence: the original funding bytes coexist with
	// paid decisions, and both readers must return reserve-backed provenance.
	a, _, e := f.read("index", "block")
	readerOK(t, e)
	b, _, e := f.read("canonical", "block")
	readerOK(t, e)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("pre-repair answers differ")
	}
	f.report.Checks = append(f.report.Checks, "before-representation equal")
	var batch protocol.RepairBatch
	readerOK(t, db.View(func(v state.ReadView) error {
		start := time.Now()
		var singles []protocol.RepairInput
		for _, out := range missing {
			target, pay, e := redaction.InputTarget(v, f.revised, policy, out, 1700000032)
			if e != nil {
				return e
			}
			var shares []chameleon.Contribution
			for _, signer := range signers[:3] {
				s, e := redaction.InputShare(v, f.revised, policy, signer, out, 1700000032)
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
		f.report.TimingNS["input_adapt"] = time.Since(start).Nanoseconds()
		start = time.Now()
		var e error
		batch, e = redaction.BuildBatch(v, f.revised, policy, singles, 1700000032)
		if e != nil {
			return e
		}
		f.report.TimingNS["batch_build"] = time.Since(start).Nanoseconds()
		start = time.Now()
		var rows [][]chameleon.Contribution
		for _, signer := range signers[:3] {
			s, e := redaction.BatchPartShares(v, f.revised, policy, signer, batch, 1700000032)
			if e != nil {
				return e
			}
			rows = append(rows, s)
		}
		batch, e = redaction.CompleteBatchParts(v, f.revised, policy, batch, 1700000032, rows)
		f.report.TimingNS["parts_adapt"] = time.Since(start).Nanoseconds()
		return e
	}))
	raw, e := batch.MarshalBinary()
	readerOK(t, e)
	f.report.Storage["repair_command_bytes"] = int64(len(raw))
	f.report.Storage["part_openings"] = int64(len(batch.Parts))
	appendBlock(1700000033, [][]byte{raw}, "representation", false)
	a, _, e = f.read("index", "block")
	readerOK(t, e)
	b, _, e = f.read("canonical", "block")
	readerOK(t, e)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("logical answers differ")
	}
	readerOK(t, db.View(func(v state.ReadView) error {
		start := time.Now()
		mat, e := redaction.PrepareMaterialization(v, batch.ID())
		f.report.TimingNS["materialization_prepare"] = time.Since(start).Nanoseconds()
		if e != nil {
			return e
		}
		start = time.Now()
		e = mat.Install(f.revised)
		f.report.TimingNS["materialization_install"] = time.Since(start).Nanoseconds()
		return e
	}))
	after, _, e := f.read("canonical", "block")
	readerOK(t, e)
	if !reflect.DeepEqual(b, after) {
		t.Fatal("installation changed answers")
	}
	if !f.original.LoadBlockMeta(1).BlockID.Equals(f.revised.LoadBlockMeta(1).BlockID) {
		t.Fatal("BlockID changed")
	}
	f.report.Checks = append(f.report.Checks, "post-repair equal", "materialization invariant", "complete BlockID stable")
	readerSnapshot(t, f, "after")
	f.report.Prefix = f.height
	f.batch = batch
	f.late = func() { appendBlock(1700000034, parentCommands, "late_sources", false) }
	return f
}

func readerSnapshot(t *testing.T, f *readerFixture, phase string) {
	readerOK(t, f.db.View(func(v state.ReadView) error {
		var after []byte
		for {
			es, e := v.(state.ScanView).Scan(nil, after, 1024)
			if e != nil {
				return e
			}
			if len(es) == 0 {
				break
			}
			for _, x := range es {
				kind := "meta"
				if len(x.Key) >= 3 && x.Key[0] == 0 && x.Key[1] == 2 {
					kind = fmt.Sprint(x.Key[2])
				}
				f.report.Storage[phase+"_app_kind_"+kind] += int64(len(x.Key) + len(x.Value))
			}
			after = es[len(es)-1].Key
		}
		return nil
	}))
	for name, db := range map[string]dbm.DB{"original": f.originalDB.DB, "revised": f.revisedDB.DB} {
		it, e := db.Iterator(nil, nil)
		readerOK(t, e)
		var size int64
		for ; it.Valid(); it.Next() {
			size += int64(len(it.Key()) + len(it.Value()))
		}
		readerOK(t, it.Error())
		readerOK(t, it.Close())
		f.report.Storage[phase+"_"+name+"_kv_bytes"] = size
	}
	readerOK(t, filepath.Walk(f.path, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if !info.IsDir() {
			f.report.Storage[phase+"_directory_bytes"] += info.Size()
		}
		return nil
	}))
}

func runReaderMeasurements(t *testing.T, f *readerFixture, samples int) {
	for _, work := range []string{"point_repaired", "point_normal", "block"} {
		rows := []readerCase{{Workload: work, Method: "index"}, {Workload: work, Method: "canonical"}}
		for i := 0; i < 20; i++ {
			for _, method := range []string{"index", "canonical"} {
				_, _, e := f.read(method, work)
				readerOK(t, e)
			}
		}
		for i := 0; i < samples; i++ {
			for k := 0; k < 2; k++ {
				j := (i + k) % 2
				_, s, e := f.read(rows[j].Method, work)
				readerOK(t, e)
				rows[j].Samples = append(rows[j].Samples, s)
			}
		}
		for j := range rows {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for i := 0; i < 30; i++ {
				_, _, e := f.read(rows[j].Method, work)
				readerOK(t, e)
			}
			runtime.ReadMemStats(&after)
			rows[j].AllocBytes = float64(after.TotalAlloc-before.TotalAlloc) / 30
			rows[j].Allocs = float64(after.Mallocs-before.Mallocs) / 30
			// Crypto only: decoded objects are prepared outside this independent timer.
			var b *types.Block
			readerOK(t, f.db.View(func(v state.ReadView) error {
				var e error
				if j == 0 {
					b, e = f.original.LoadOriginalBlock(1)
				} else {
					b, _, e = redaction.Canonical(v, f.revised, 1)
				}
				return e
			}))
			ids := []int{0}
			if work == "point_normal" {
				ids[0] = f.count - 1
			}
			if work == "block" {
				ids = make([]int, f.count)
				for k := range ids {
					ids[k] = k
				}
			}
			var pays []protocol.DirectSubmission
			for _, k := range ids {
				pay, e := protocol.DecodeDirectSubmission(b.Data.Txs[k])
				readerOK(t, e)
				pays = append(pays, pay)
			}
			for i := 0; i < 15; i++ {
				start := time.Now()
				for _, p := range pays {
					readerOK(t, readerCrypto(p, f.policy))
				}
				rows[j].CryptoNS = append(rows[j].CryptoNS, time.Since(start).Nanoseconds())
				start = time.Now()
				for _, p := range pays {
					readerOK(t, readerInputCH(p, f.policy))
				}
				rows[j].InputCHNS = append(rows[j].InputCHNS, time.Since(start).Nanoseconds())
			}
		}
		f.report.Cases = append(f.report.Cases, rows...)
	}
	// Representation-only complete identity recomputation, with committed
	// bodies and expected BlockID prepared outside the timed operation.
	f.report.IdentityNS = map[string][]int64{}
	readerOK(t, f.db.View(func(v state.ReadView) error {
		old, e := f.original.LoadOriginalBlock(1)
		if e != nil {
			return e
		}
		current, revision, e := redaction.Canonical(v, f.revised, 1)
		if e != nil {
			return e
		}
		id := f.original.LoadBlockMeta(1).BlockID
		for i := 0; i < 15; i++ {
			start := time.Now()
			parts, e := old.MakePartSet(types.BlockPartSizeBytes)
			if e != nil {
				return e
			}
			if !id.Equals(types.BlockID{Hash: old.Hash(), PartSetHeader: parts.Header()}) {
				return protocol.ErrAuth
			}
			f.report.IdentityNS["original"] = append(f.report.IdentityNS["original"], time.Since(start).Nanoseconds())
			start = time.Now()
			parts, e = types.NewRedactablePartSet(revision.Body, types.BlockPartSizeBytes, 1, revision.Number, f.batch.Parts)
			if e != nil {
				return e
			}
			if !id.Equals(types.BlockID{Hash: current.Hash(), PartSetHeader: parts.Header()}) {
				return protocol.ErrAuth
			}
			f.report.IdentityNS["canonical"] = append(f.report.IdentityNS["canonical"], time.Since(start).Nanoseconds())
		}
		return nil
	}))
}

func readerRecoveryCheck(t *testing.T, f *readerFixture) {
	// Functional only, after all benchmark snapshots/timers. Recovery must not
	// erase provenance or require a second adaptation.
	f.late()
	a, _, e := f.read("index", "block")
	readerOK(t, e)
	b, _, e := f.read("canonical", "block")
	readerOK(t, e)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("recovery answers differ")
	}
	for _, x := range a[:f.repaired] {
		if x.Status != rules.DirectRecovered || x.Kind != protocol.ReserveFunding {
			t.Fatal("recovery lost provenance")
		}
	}
	f.report.Checks = append(f.report.Checks, "recovery keeps paid provenance and equal answers")
}

func TestHistoricalReaderEquivalence(t *testing.T) {
	f := newReaderFixture(t, t.TempDir(), 4, 1)
	for _, work := range []string{"point_repaired", "point_normal", "block"} {
		a, _, e := f.read("index", work)
		readerOK(t, e)
		b, _, e := f.read("canonical", work)
		readerOK(t, e)
		if !reflect.DeepEqual(a, b) {
			t.Fatal(work)
		}
	}
	readerNegativeChecks(t, f)
	readerRecoveryCheck(t, f)
}

func TestHistoricalReaderExperiment(t *testing.T) {
	root := os.Getenv("UTXO_READER_RESULTS")
	if root == "" {
		t.Skip("set UTXO_READER_RESULTS to run the offline cost experiment")
	}
	for _, n := range []int{32, 256} {
		for _, m := range []int{1, 8} {
			t.Run(fmt.Sprintf("n%d_m%d", n, m), func(t *testing.T) {
				path := filepath.Join(root, fmt.Sprintf("n%d_m%d", n, m))
				if _, e := os.Stat(path); !os.IsNotExist(e) {
					t.Fatal("refusing to overwrite experiment", path)
				}
				f := newReaderFixture(t, path, n, m)
				readerNegativeChecks(t, f)
				runReaderMeasurements(t, f, 300)
				readerRecoveryCheck(t, f)
				raw, e := json.MarshalIndent(f.report, "", "  ")
				readerOK(t, e)
				readerOK(t, os.WriteFile(path+".json", raw, 0644))
			})
		}
	}
}
