package committee_test

import (
	"bytes"
	"context"
	"fmt"
	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
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

func TestFinalC3MembersLedgerAndReservedSource(t *testing.T) {
	f, c := c3EngineConfig(t)
	final := c.Direct.C3
	final.Policies[0].Version = 2
	final.Policies[0].Schedule = protocol.FeeSchedule{AttachReward: 1, ExecuteReward: 2, ResolveReward: 3, Compensate: 4, Repay: 5}
	final.Policies[0].Tiers = []rules.C3Tier{{ID: protocol.Digest("priority-tier"), CAL: 1, MinimumReward: 2}}
	final.EpochBlocks = 2
	final.ReservedBPS = 2500
	address := bytes.Repeat([]byte{1}, 20)
	validator := protocol.Digest("C3_VALIDATOR", address)
	final.Committee = []rules.C3Validator{{ID: validator, Address: address, Weight: 1}}
	f.Genesis.Grants[len(f.Genesis.Grants)-1].Amount = 45
	c.Genesis = f.Genesis
	c.Accounts[2].Balance = 45
	policy, err := c.Direct.Policy(f.Schedule, c.Organizations)
	if err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(t.TempDir(), "ledger.db")
	ledgerID := store.Identity{Network: "c3-final", Role: "committee", Node: "0", Schema: 9}
	db, err := store.Open(ledgerPath, ledgerID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	engine, err := committee.NewEngine(c, db)
	if err != nil {
		t.Fatal(err)
	}
	var members []*member.Member
	var stores []store.Store
	var memberPaths []string
	defer func() {
		for _, s := range stores {
			s.Close()
		}
	}()
	for i := 0; i < 3; i++ {
		path := filepath.Join(t.TempDir(), "member.db")
		memberPaths = append(memberPaths, path)
		md, err := store.Open(path, store.Identity{Network: "c3-final", Role: "member", Node: fmt.Sprint(i), Schema: 9})
		if err != nil {
			t.Fatal(err)
		}
		m, err := member.New(member.Config{Organization: f.Org, Index: uint16(i), Key: f.Keys[i], Peers: c.Organizations, Schedule: f.Schedule, Workers: 1, Direct: c.Direct, DisableSourceRecovery: true}, md, f.Genesis)
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, m)
		stores = append(stores, md)
	}
	certify := func(r protocol.DirectRequest) protocol.DirectPayment {
		t.Helper()
		p := protocol.DirectPayment{Tx: r.Tx, InputCertificates: r.InputCertificates}
		for _, m := range members {
			a, e := m.ApproveDirect(context.Background(), r)
			if e != nil {
				t.Fatal(e)
			}
			p.Certificate.Summary = a.Summary
			p.Certificate.QC.Fact = a.Summary.Fact()
			p.Certificate.QC.Votes = append(p.Certificate.QC.Votes, a.Vote)
		}
		return p
	}
	source := certify(c3Build(t, f, policy, 0, nil))
	child := certify(c3Build(t, f, policy, 1, &source.Certificate))
	third := c3Build(t, f, policy, 2, nil)
	if _, e := members[0].ApproveDirect(context.Background(), third); e == nil {
		t.Fatal("hidden final budgets escaped atomic reserve")
	}
	var previous []byte
	execute := func(p protocol.DirectPayment, h int64) {
		t.Helper()
		raw, e := p.Submission().MarshalBinary()
		if e != nil {
			t.Fatal(e)
		}
		var tr state.Transition
		if e = db.Update(func(v state.ReadView) ([]state.Change, error) {
			begin, e := engine.BeginBlock(v, committee.BlockContext{Height: h, Time: time.Unix(100+h, 0)})
			if e != nil {
				return nil, e
			}
			o := state.NewOverlay(v)
			o.Apply(begin.Changes)
			tr, e = engine.ExecuteAt(o, raw, committee.BlockContext{Height: h, Time: time.Unix(100+h, 0), ProposerAddress: address})
			o.Apply(tr.Changes)
			return o.Changes(), e
		}); e != nil {
			t.Fatal(e)
		}
		trust, data := testkit.Block("c3-engine", h, previous, [][]byte{raw}, []*abci.ExecTxResult{{Data: tr.Data}})
		b, e := finality.VerifyBlock(trust, data)
		if e != nil {
			t.Fatal(e)
		}
		for i, m := range members {
			if e = blockfollow.Commit(stores[i], b, m.PrepareBlock); e != nil {
				t.Fatal(e)
			}
			if e = blockfollow.Commit(stores[i], b, m.PrepareBlock); e != nil {
				t.Fatal(e)
			}
		}
		previous = b.Hash()
	}
	execute(child, 1)
	// A process restart must retain hidden approvals, output events, and funds.
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ledgerPath, ledgerID)
	if err != nil {
		t.Fatal(err)
	}
	engine, err = committee.NewEngine(c, db)
	if err != nil {
		t.Fatal(err)
	}
	for i := range members {
		if err = stores[i].Close(); err != nil {
			t.Fatal(err)
		}
		md, e := store.Open(memberPaths[i], store.Identity{Network: "c3-final", Role: "member", Node: fmt.Sprint(i), Schema: 9})
		if e != nil {
			t.Fatal(e)
		}
		stores[i] = md
		members[i], e = member.New(member.Config{Organization: f.Org, Index: uint16(i), Key: f.Keys[i], Peers: c.Organizations, Schedule: f.Schedule, Workers: 1, Direct: c.Direct, DisableSourceRecovery: true}, md, f.Genesis)
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, e := members[0].ApproveDirect(context.Background(), third); e == nil {
		t.Fatal("restart discarded hidden approval reserve")
	}
	sourceRaw, _ := source.Submission().MarshalBinary()
	// This ordinary request is validly certified but not approved in member state;
	// the proposer only classifies public authorization, not private approval logs.
	ordinary := protocol.DirectPayment{Tx: third.Tx, Certificate: protocol.OutputCertificate{}}
	vector, e := rules.PrepareDirectVector(third.Tx, policy)
	if e != nil {
		t.Fatal(e)
	}
	ordinary.Certificate.Summary = protocol.SummaryFor(third.Tx, vector)
	ordinary.Certificate.QC.Fact = ordinary.Certificate.Summary.Fact()
	for i := 0; i < 3; i++ {
		ordinary.Certificate.QC.Votes = append(ordinary.Certificate.QC.Votes, protocol.SignSpend(ordinary.Certificate.QC.Fact, uint16(i), f.Keys[i]))
	}
	ordinaryRaw, _ := ordinary.Submission().MarshalBinary()
	paidBody := third.Tx.Body
	paidBody.Outputs = append([]protocol.Output(nil), paidBody.Outputs...)
	paidBody.Fee.C3.Tier = final.Policies[0].Tiers[0].ID
	var refs []protocol.AdmissionRef
	for _, g := range f.Genesis.Grants {
		refs = append(refs, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
	}
	if e = rules.BindC3(&paidBody, policy, refs); e != nil {
		t.Fatal(e)
	}
	paidBody.Outputs[0].Amount--
	paidTx, e := protocol.NewFastTx(paidBody, third.Tx.Claims, policy.Key)
	if e != nil {
		t.Fatal(e)
	}
	paidTx.Auth = []protocol.OwnerAuth{protocol.SignOwner(paidTx.ID(), f.Owner)}
	paid := protocol.DirectPayment{Tx: paidTx}
	paidVector, e := rules.PrepareDirectVector(paidTx, policy)
	if e != nil {
		t.Fatal(e)
	}
	paid.Certificate.Summary = protocol.SummaryFor(paidTx, paidVector)
	paid.Certificate.QC.Fact = paid.Certificate.Summary.Fact()
	for i := 0; i < 3; i++ {
		paid.Certificate.QC.Votes = append(paid.Certificate.QC.Votes, protocol.SignSpend(paid.Certificate.QC.Fact, uint16(i), f.Keys[i]))
	}
	paidRaw, e := paid.Submission().MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	idle, e := engine.SelectProposal([][]byte{ordinaryRaw, paidRaw}, cmttypes.ComputeProtoSizeForTxs([]cmttypes.Tx{ordinaryRaw, paidRaw}))
	if e != nil || len(idle) != 2 || !bytes.Equal(idle[0], paidRaw) {
		t.Fatal("ordinary priority/idle reserved reuse", e)
	}
	selected, e := engine.SelectProposal([][]byte{ordinaryRaw, sourceRaw}, cmttypes.ComputeProtoSizeForTxs([]cmttypes.Tx{sourceRaw}))
	if e != nil || len(selected) != 1 || !bytes.Equal(selected[0], sourceRaw) {
		t.Fatal("reserved source excluded", e)
	}
	execute(source, 2)
	if _, e = members[0].ApproveDirect(context.Background(), third); e != nil {
		t.Fatal("unused final budget did not release", e)
	}
	snap, e := engine.C3Fee(source.Certificate.Summary.Fact())
	if e != nil || snap.Record.Escrow.Rewards != 6 || snap.Record.Escrow.Burned != 0 || snap.Pool.PoolReflow != 0 {
		t.Fatalf("snapshot %+v %v", snap, e)
	}
	if e = db.View(func(v state.ReadView) error {
		amount, _, e := state.Load[uint64](v, state.Key(state.KeyReward, validator[:]))
		if amount != 12 {
			t.Fatalf("wrong includer/shared total %d", amount)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}

func TestFinalC3ConcurrentHiddenApprovals(t *testing.T) {
	f, c := c3EngineConfig(t)
	settings := c.Direct.C3
	settings.Policies[0].Version = 2
	settings.Policies[0].Schedule = protocol.FeeSchedule{AttachReward: 1, ExecuteReward: 1, ResolveReward: 2, Compensate: 1, Repay: 1}
	settings.EpochBlocks = 2
	settings.ReservedBPS = 2500
	settings.Committee = []rules.C3Validator{{ID: protocol.Digest("validator"), Address: bytes.Repeat([]byte{1}, 20), Weight: 1}}
	f.Genesis.Grants[len(f.Genesis.Grants)-1].Amount = 18
	p, e := c.Direct.Policy(f.Schedule, c.Organizations)
	if e != nil {
		t.Fatal(e)
	}
	db := store.NewMemory()
	defer db.Close()
	m, e := member.New(member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Peers: c.Organizations, Schedule: f.Schedule, Workers: 1, Direct: c.Direct}, db, f.Genesis)
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
	for e := range results {
		if e == nil {
			approved++
		}
	}
	if approved != 2 {
		t.Fatalf("concurrent final budgets admitted %d", approved)
	}
}
