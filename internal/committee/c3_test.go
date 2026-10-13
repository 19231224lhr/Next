package committee_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	abci "github.com/cometbft/cometbft/abci/types"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/committee"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func c3Build(t *testing.T, f testkit.Fixture, p rules.DirectPolicy, index int, parent *protocol.OutputCertificate) protocol.DirectRequest {
	t.Helper()
	legacy := f
	legacy.Genesis.Grants = f.Genesis.Grants[:len(f.Genesis.Grants)-1]
	old := p
	old.C3 = nil
	tx, e := legacy.FastTransaction(index, uint64(index+1), old)
	if e != nil {
		t.Fatal(e)
	}
	b := tx.Body
	var proofs []protocol.InputCertificate
	if parent != nil {
		b.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: parent.Summary.OutputID(0), Evidence: protocol.Hash(parent.QC.Fact)}}
		proofs = []protocol.InputCertificate{{Certificate: *parent}}
	}
	var refs []protocol.AdmissionRef
	for _, g := range f.Genesis.Grants {
		refs = append(refs, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
	}
	if e = rules.BindC3(&b, p, refs); e != nil {
		t.Fatal(e)
	}
	tx, e = protocol.NewFastTx(b, tx.Claims, p.Key)
	if e != nil {
		t.Fatal(e)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.Owner)}
	return protocol.DirectRequest{Tx: tx, InputCertificates: proofs}
}

func TestC3MembersEngineDurableHiddenReservations(t *testing.T) {
	for _, compensate := range []bool{false, true} {
		t.Run(map[bool]string{false: "fulfilled", true: "compensated-late"}[compensate], func(t *testing.T) { c3Durable(t, compensate) })
	}
}
func c3Durable(t *testing.T, compensate bool) {
	f, c := c3EngineConfig(t)
	last := len(f.Genesis.Grants) - 1
	f.Genesis.Grants[last].Amount = 18
	c.Genesis = f.Genesis
	c.Accounts[2].Balance = 18
	policy, e := c.Direct.Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	lid := store.Identity{Network: "c3-engine", Role: "committee", Node: "0", Schema: 9}
	mid := store.Identity{Network: "c3-engine", Role: "member", Node: "0", Schema: 9}
	lp, mp := filepath.Join(t.TempDir(), "ledger.db"), filepath.Join(t.TempDir(), "member.db")
	ledger, e := store.Open(lp, lid)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { ledger.Close() }()
	md, e := store.Open(mp, mid)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { md.Close() }()
	engine, e := committee.NewEngine(c, ledger)
	if e != nil {
		t.Fatal(e)
	}
	mc := member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Peers: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Workers: 1, Direct: c.Direct, DisableSourceRecovery: true}
	m, e := member.New(mc, md, f.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	others := []*member.Member{m}
	for i := 1; i < 3; i++ {
		db := store.NewMemory()
		defer db.Close()
		cfg := mc
		cfg.Index = uint16(i)
		cfg.Key = f.Keys[i]
		mm, e := member.New(cfg, db, f.Genesis)
		if e != nil {
			t.Fatal(e)
		}
		others = append(others, mm)
	}
	certify := func(r protocol.DirectRequest) protocol.DirectPayment {
		t.Helper()
		out := protocol.DirectPayment{Tx: r.Tx, InputCertificates: r.InputCertificates}
		for _, mm := range others {
			a, e := mm.ApproveDirect(context.Background(), r)
			if e != nil {
				t.Fatal(e)
			}
			out.Certificate.Summary = a.Summary
			out.Certificate.QC.Fact = a.Summary.Fact()
			out.Certificate.QC.Votes = append(out.Certificate.QC.Votes, a.Vote)
		}
		return out
	}
	source := certify(c3Build(t, f, policy, 0, nil))
	child := certify(c3Build(t, f, policy, 1, &source.Certificate))
	third := c3Build(t, f, policy, 2, nil)
	if _, e = m.ApproveDirect(context.Background(), third); e == nil {
		t.Fatal("hidden obligations exceeded 2B/3")
	}
	if e = md.View(func(v state.ReadView) error {
		s, _, err := state.Load[state.Spend](v, rules.DirectSpendKey(third.Tx.Body.Inputs[0].Output, 0))
		if s.Candidate != (protocol.SpendFactID{}) {
			t.Fatal("failed reserve leaked principal lock")
		}
		return err
	}); e != nil {
		t.Fatal(e)
	}
	var previous []byte
	height := int64(0)
	execute := func(pay protocol.DirectPayment) {
		t.Helper()
		height++
		raw, e := pay.Submission().MarshalBinary()
		if e != nil {
			t.Fatal(e)
		}
		var tr state.Transition
		e = ledger.Update(func(v state.ReadView) ([]state.Change, error) {
			var err error
			tr, err = engine.ExecuteAt(v, raw, committee.BlockContext{Height: height, Time: c3Time()})
			return tr.Changes, err
		})
		if e != nil {
			t.Fatal(e)
		}
		trust, data := testkit.Block("c3-engine", height, previous, [][]byte{raw}, []*abci.ExecTxResult{{Data: tr.Data}})
		b, e := finality.VerifyBlock(trust, data)
		if e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 2; i++ {
			if e = blockfollow.Commit(md, b, m.PrepareBlock); e != nil {
				t.Fatal(e)
			}
		}
		previous = b.Hash()
	}
	execute(child)
	snap, e := engine.C3Fee(source.Certificate.QC.Fact)
	if e != nil || snap.Record.QuoteCollected || snap.Record.Escrow.Held != 4 {
		t.Fatalf("source escrow %+v %v", snap, e)
	}
	if compensate {
		// Anchor the missing input using the real begin-block transition.
		if e = ledger.Update(func(v state.ReadView) ([]state.Change, error) {
			tr, err := engine.BeginBlock(v, committee.BlockContext{Height: 2, Time: time.Unix(101, 0)})
			return tr.Changes, err
		}); e != nil {
			t.Fatal(e)
		}
		var tr state.Transition
		if e = ledger.Update(func(v state.ReadView) ([]state.Change, error) {
			var err error
			tr, err = rules.EvaluateDirectCompensation(v, source.Certificate.Summary.OutputID(0), policy, 132)
			return tr.Changes, err
		}); e != nil {
			t.Fatal(e)
		}
		cmd := protocol.CompensationDecision{Network: f.Org.Network, Output: source.Certificate.Summary.OutputID(0), Height: 1}
		raw, err := cmd.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		dataRaw, err := (protocol.CompensationResult{Decision: cmd.ID(), Applied: true, Effects: []protocol.RepairEffect{{Output: cmd.Output, ParentFact: source.Certificate.QC.Fact, ConsumerFact: child.Certificate.QC.Fact, ConsumerTx: child.Tx.ID(), Amount: 100, Debit: protocol.ReserveDebitIdentity(f.Org.Network, cmd.Output)}}}).MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		height++
		trust, data := testkit.Block("c3-engine", height, previous, [][]byte{raw}, []*abci.ExecTxResult{{Data: dataRaw}})
		b, err := finality.VerifyBlock(trust, data)
		if err != nil {
			t.Fatal(err)
		}
		if err = blockfollow.Commit(md, b, m.PrepareBlock); err != nil {
			t.Fatal(err)
		}
		previous = b.Hash()
		snap, err = engine.C3Fee(source.Certificate.QC.Fact)
		if err != nil || snap.Record.Escrow.Held != 1 || snap.Record.QuoteCollected {
			t.Fatalf("compensated source %+v %v", snap, err)
		}
	}
	if e = md.Close(); e != nil {
		t.Fatal(e)
	}
	md, e = store.Open(mp, mid)
	if e != nil {
		t.Fatal(e)
	}
	m, e = member.New(mc, md, f.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	if e = ledger.Close(); e != nil {
		t.Fatal(e)
	}
	ledger, e = store.Open(lp, lid)
	if e != nil {
		t.Fatal(e)
	}
	engine, e = committee.NewEngine(c, ledger)
	if e != nil {
		t.Fatal(e)
	}
	execute(source)
	snap, e = engine.C3Fee(source.Certificate.QC.Fact)
	wantBurn, wantRelease, wantReserved := uint64(2), uint64(2), uint64(6)
	if compensate {
		wantBurn, wantRelease, wantReserved = 4, 0, 8
	}
	if e != nil || snap.Record.Escrow.Burned != wantBurn || snap.Record.Escrow.Released != wantRelease || snap.Pool.CumulativeFees != 2000 || snap.Pool.Released != 4+wantRelease {
		t.Fatalf("settled snapshot %+v %v", snap, e)
	}
	if e = md.View(func(v state.ReadView) error {
		slice, _, err := state.Load[state.Slice](v, state.SliceKey(c.Direct.C3.Issuers[0].Funding.Key, 0))
		if slice.Reserved != wantReserved || slice.Available != 12-wantReserved {
			t.Fatalf("spent fees restored %+v", slice)
		}
		return err
	}); e != nil {
		t.Fatal(e)
	}
	if compensate {
		if _, e = m.ApproveDirect(context.Background(), third); e == nil {
			t.Fatal("spent compensation fuel restored")
		}
		return
	}
	if _, e = m.ApproveDirect(context.Background(), third); e != nil {
		t.Fatal("unused release not reusable", e)
	}
}

func c3Time() time.Time { return time.Unix(100, 0) }

func c3EngineConfig(t *testing.T) (testkit.Fixture, committee.EngineConfig) {
	t.Helper()
	f := testkit.NewFixture("c3-engine", "org", 3)
	f.EnableDirect()
	raw, e := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := pem.Decode(raw)
	key, e := x509.ParsePKCS1PrivateKey(b.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	pool := protocol.Digest("c3-pool")
	g := state.Grant{ID: protocol.Digest("c3-grant"), Organization: f.Org.Hash(), Key: protocol.ResourceKey{Kind: protocol.ResourceFUEL, Account: pool, Version: 1}, Amount: 100, Subject: f.Genesis.Outputs[0].Output.Recipient.Owner}
	f.Genesis.Grants = append(f.Genesis.Grants, g)
	c := committee.EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Direct: &rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5, C3: &rules.C3Settings{Current: protocol.Digest("c3-policy"), Policies: []rules.C3Policy{{ID: protocol.Digest("c3-policy"), Schedule: protocol.FeeSchedule{AttachReward: 1, ResolveReward: 1, Open: 1, Close: 1, Compensate: 1, Repay: 1}}}, Issuers: []rules.C3Issuer{{Config: f.Org.Hash(), Funding: protocol.AdmissionRef{Key: g.Key, Grant: g.ID}, Revenue: protocol.Digest("operating"), Beneficiary: protocol.Digest("reward")}}}}, Accounts: []committee.GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetCAL, Balance: 1000000000}, {Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1000000000}, {Owner: pool, Asset: protocol.AssetFUEL, Balance: 100}, {Owner: protocol.ReserveFundingAccount(f.Org.Network, g.Subject), Asset: protocol.AssetFUEL, Balance: 100}}}
	return f, c
}

func TestC3FundedPoolSnapshotAndTopup(t *testing.T) {
	f, c := c3EngineConfig(t)
	db := store.NewMemory()
	defer db.Close()
	engine, e := committee.NewEngine(c, db)
	if e != nil {
		t.Fatal(e)
	}
	issuer := c.Direct.C3.Issuers[0]
	snap, e := engine.C3Pool(issuer.Funding.Key)
	if e != nil || snap.OriginalFunding != 100 || snap.Funding != 100 || snap.Free != 100 {
		t.Fatalf("snapshot %+v %v", snap, e)
	}
	cmd := protocol.ReserveIncrease{Network: f.Org.Network, Organization: f.Org.Hash(), Grant: issuer.Funding.Grant, Key: issuer.Funding.Key, Previous: 100, Amount: 30}
	cmd.Sign(f.Owner)
	raw, e := cmd.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, e := engine.ExecuteAt(v, raw, committee.BlockContext{Height: 1, Time: c3Time()})
		return tr.Changes, e
	}); e != nil {
		t.Fatal(e)
	}
	snap, e = engine.C3Pool(issuer.Funding.Key)
	if e != nil || snap.OriginalFunding != 100 || snap.Funding != 130 || snap.Free != 130 {
		t.Fatalf("topup %+v %v", snap, e)
	}
	cmd.Previous, cmd.Amount = 130, 71
	cmd.Sign(f.Owner)
	raw, e = cmd.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, e := engine.ExecuteAt(v, raw, committee.BlockContext{Height: 2, Time: c3Time()})
		return tr.Changes, e
	}); e == nil {
		t.Fatal("unfunded topup succeeded")
	}
	snap, e = engine.C3Pool(issuer.Funding.Key)
	if e != nil || snap.Funding != 130 || snap.Free != 130 {
		t.Fatal("failed topup mutated pool", snap, e)
	}
}

func TestC3GenesisRejectsUnboundIssuerGrant(t *testing.T) {
	_, c := c3EngineConfig(t)
	c.Direct.C3.Issuers[0].Funding.Grant = protocol.Digest("fake")
	db := store.NewMemory()
	defer db.Close()
	if _, e := committee.NewEngine(c, db); e == nil {
		t.Fatal("accepted fake issuer grant")
	}
}

func TestC3GenesisRejectsUnbackedOrAliasedPool(t *testing.T) {
	for _, kind := range []string{"underfunded", "double-count", "revenue-alias", "funding-alias"} {
		t.Run(kind, func(t *testing.T) {
			f, c := c3EngineConfig(t)
			i := &c.Direct.C3.Issuers[0]
			switch kind {
			case "underfunded":
				c.Accounts[2].Balance = 99
			case "double-count":
				g := c.Genesis.Grants[len(c.Genesis.Grants)-1]
				g.Key.Version++
				g.ID = protocol.Digest("duplicate-cash")
				c.Genesis.Grants = append(c.Genesis.Grants, g)
			case "revenue-alias":
				i.Revenue = i.Funding.Key.Account
			case "funding-alias":
				pool := protocol.ReserveFundingAccount(f.Org.Network, c.Genesis.Grants[len(c.Genesis.Grants)-1].Subject)
				i.Funding.Key.Account = pool
				c.Genesis.Grants[len(c.Genesis.Grants)-1].Key.Account = pool
				c.Accounts[2].Owner = pool
				c.Accounts = c.Accounts[:3]
			}
			db := store.NewMemory()
			defer db.Close()
			if _, e := committee.NewEngine(c, db); e == nil {
				t.Fatal("invalid backing accepted")
			}
		})
	}
}

func TestC3CrossOrgChargesDirectSourceAndBoundsGrandchild(t *testing.T) {
	a, c := c3EngineConfig(t)
	b := testkit.NewFixture("c3-engine", "other", 1)
	b.EnableDirect()
	bg := state.Grant{ID: protocol.Digest("other-c3-grant"), Organization: b.Org.Hash(), Key: protocol.ResourceKey{Kind: protocol.ResourceFUEL, Account: protocol.Digest("other-c3-pool"), Version: 1}, Amount: 100, Subject: b.Genesis.Outputs[0].Output.Recipient.Owner}
	b.Genesis.Grants = append(b.Genesis.Grants, bg)
	c.Organizations = append(c.Organizations, b.Org)
	c.Genesis.Outputs = append(c.Genesis.Outputs, b.Genesis.Outputs...)
	c.Genesis.Grants = append(c.Genesis.Grants, b.Genesis.Grants...)
	c.Accounts = append(c.Accounts, committee.GenesisAccount{Owner: b.Org.Org, Asset: protocol.AssetCAL, Balance: 1000000000}, committee.GenesisAccount{Owner: b.Org.Org, Asset: protocol.AssetFUEL, Balance: 1000000000}, committee.GenesisAccount{Owner: bg.Key.Account, Asset: protocol.AssetFUEL, Balance: 100})
	c.Direct.C3.Issuers = append(c.Direct.C3.Issuers, rules.C3Issuer{Config: b.Org.Hash(), Funding: protocol.AdmissionRef{Key: bg.Key, Grant: bg.ID}, Revenue: protocol.Digest("other-revenue"), Beneficiary: c.Direct.C3.Issuers[0].Beneficiary})
	p, e := c.Direct.Policy(c.Schedule, c.Organizations)
	if e != nil {
		t.Fatal(e)
	}
	db := store.NewMemory()
	defer db.Close()
	engine, e := committee.NewEngine(c, db)
	if e != nil {
		t.Fatal(e)
	}
	r := c3Build(t, a, p, 0, nil)
	body := r.Tx.Body
	body.Outputs[0].Recipient = b.Genesis.Outputs[0].Output.Recipient
	tx, e := protocol.NewFastTx(body, r.Tx.Claims, p.Key)
	if e != nil {
		t.Fatal(e)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), a.Owner)}
	cert, e := a.DirectCertificate(tx, p)
	if e != nil {
		t.Fatal(e)
	}
	source := protocol.DirectPayment{Tx: tx, Certificate: cert}
	r = c3Build(t, b, p, 0, &source.Certificate)
	cert, e = b.DirectCertificate(r.Tx, p)
	if e != nil {
		t.Fatal(e)
	}
	child := protocol.DirectPayment{Tx: r.Tx, Certificate: cert, InputCertificates: r.InputCertificates}
	r = c3Build(t, b, p, 0, &child.Certificate)
	cert, e = b.DirectCertificate(r.Tx, p)
	if e != nil {
		t.Fatal(e)
	}
	grand := protocol.DirectPayment{Tx: r.Tx, Certificate: cert, InputCertificates: r.InputCertificates}
	execute := func(pay protocol.DirectPayment) {
		t.Helper()
		raw, e := pay.Submission().MarshalBinary()
		if e != nil {
			t.Fatal(e)
		}
		if e = db.Update(func(v state.ReadView) ([]state.Change, error) {
			tr, e := engine.ExecuteAt(v, raw, committee.BlockContext{Height: 1, Time: c3Time()})
			return tr.Changes, e
		}); e != nil {
			t.Fatal(e)
		}
	}
	execute(child)
	before, e := engine.C3Fee(source.Certificate.QC.Fact)
	if e != nil {
		t.Fatal(e)
	}
	execute(grand)
	after, e := engine.C3Fee(source.Certificate.QC.Fact)
	if e != nil {
		t.Fatal(e)
	}
	if before.Record != after.Record || after.Record.Escrow.Burned != 1 || after.Record.Escrow.Rewards != 1 || after.Record.QuoteCollected {
		t.Fatalf("grandchild changed direct-source fees %+v", after.Record)
	}
	if after.Pool.Spent != 2 || after.Pool.CumulativeFees != 0 {
		t.Fatalf("cross-org fee attribution %+v", after.Pool)
	}
	execute(source)
	after, e = engine.C3Fee(source.Certificate.QC.Fact)
	if e != nil || after.Pool.Spent != 4 || after.Pool.CumulativeFees != 1000 {
		t.Fatalf("source payment %+v %v", after, e)
	}
}

func TestC3ConcurrentHiddenApprovalsAndAtomicPolicyFailure(t *testing.T) {
	f, c := c3EngineConfig(t)
	f.Genesis.Grants[len(f.Genesis.Grants)-1].Amount = 18
	p, e := c.Direct.Policy(f.Schedule, c.Organizations)
	if e != nil {
		t.Fatal(e)
	}
	cfg := member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Peers: c.Organizations, Schedule: f.Schedule, Workers: 1, Direct: c.Direct}
	db := store.NewMemory()
	defer db.Close()
	m, e := member.New(cfg, db, f.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	requests := make([]protocol.DirectRequest, 3)
	for i := range requests {
		requests[i] = c3Build(t, f, p, i, nil)
	}
	results := make(chan error, 3)
	var wg sync.WaitGroup
	for _, r := range requests {
		wg.Add(1)
		go func(r protocol.DirectRequest) {
			defer wg.Done()
			_, e := m.ApproveDirect(context.Background(), r)
			results <- e
		}(r)
	}
	wg.Wait()
	close(results)
	approved := 0
	for err := range results {
		if err == nil {
			approved++
		}
	}
	if approved != 2 {
		t.Fatalf("concurrent cap allowed %d requests", approved)
	}
	for i := range f.Genesis.Grants {
		if f.Genesis.Grants[i].Key.Kind == protocol.ResourcePolicy {
			f.Genesis.Grants[i].Subject = protocol.PublicKey{1}
		}
	}
	badDB := store.NewMemory()
	defer badDB.Close()
	bad, e := member.New(cfg, badDB, f.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = bad.ApproveDirect(context.Background(), requests[0]); e == nil {
		t.Fatal("unauthorized sponsor subject accepted")
	}
	if e = badDB.View(func(v state.ReadView) error {
		s, _, err := state.Load[state.Slice](v, state.SliceKey(c.Direct.C3.Issuers[0].Funding.Key, 0))
		if s.Reserved != 0 || s.Available != 12 {
			t.Fatalf("failed sponsor policy left issuer lock %+v", s)
		}
		return err
	}); e != nil {
		t.Fatal(e)
	}
}
