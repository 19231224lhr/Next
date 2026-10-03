//go:build comet_v3

package redaction_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	abci "github.com/cometbft/cometbft/abci/types"
	cmted "github.com/cometbft/cometbft/crypto/ed25519"
	cmtstore "github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
	"utxo/finality"
	"utxo/internal/blockfollow"
	apppkg "utxo/internal/committee"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

// This is a deterministic protocol experiment, not a network benchmark. Votes
// come from real Member.ApproveDirect calls; public commands use Engine.ExecuteAt
// and authenticated execution results are applied through blockfollow.Commit.
type boundaryLab struct {
	f           [2]testkit.Fixture
	p           rules.DirectPolicy
	m           [2][4]*member.Member
	db          [2][4]store.Store
	ledger      store.Store
	engine      *apppkg.Engine
	app         *apppkg.App
	appHash     []byte
	blocks      *cmtstore.BlockStore
	vals        *types.ValidatorSet
	keys        map[string]cmted.PrivKey
	last        *types.Commit
	pending     *types.Block
	lastResults []*abci.ExecTxResult
	height      int64
	fees        [2][]state.OriginOutput
	baseline    [2]int64
}

func newBoundaryLab(t *testing.T, denySponsor ...bool) *boundaryLab {
	t.Helper()
	_, _, vals, keys := committee(t)
	x := &boundaryLab{vals: vals, keys: keys, last: &types.Commit{}}
	x.f[0], x.f[1] = testkit.NewFixture(chain, "boundary-a", 5), testkit.NewFixture(chain, "boundary-b", 5)
	x.f[1].Owner = x.f[0].Owner
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := pem.Decode(raw)
	rsa, err := x509.ParsePKCS1PrivateKey(pb.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	settings := rules.DirectSettings{Modulus: rsa.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	genesis := state.Genesis{Network: x.f[0].Org.Network}
	var accounts []apppkg.GenesisAccount
	for j := range x.f {
		f := &x.f[j]
		desc := protocol.NewDescriptor(f.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: f.Org.Org}, f.Owner)
		for i := range f.Genesis.Outputs {
			f.Genesis.Outputs[i].Output.Recipient = desc
		}
		for i := range f.Genesis.Grants {
			f.Genesis.Grants[i].Subject = desc.Owner
			if j == 0 && len(denySponsor) > 0 && denySponsor[0] && f.Genesis.Grants[i].Key.Kind == protocol.ResourcePolicy {
				other := testkit.NewFixture(chain, "unauthorized-policy-subject", 1)
				f.Genesis.Grants[i].Subject = other.Genesis.Outputs[0].Output.Recipient.Owner
			}
		}
		f.EnableDirect()
		if j == 0 {
			f.Genesis.Grants[0].Amount = 300
		}
		for i := 0; i < 20; i++ {
			fee := state.OriginOutput{ID: protocol.OutputID(protocol.Digest("boundary-fee", f.Org.Org[:], []byte(fmt.Sprint(i)))), Fact: protocol.Digest("boundary-fee-fact", f.Org.Org[:], []byte(fmt.Sprint(i))), Output: protocol.Output{Asset: protocol.AssetFUEL, Amount: 10000, Recipient: desc}}
			x.fees[j] = append(x.fees[j], fee)
			f.Genesis.Outputs = append(f.Genesis.Outputs, fee)
		}
		genesis.Outputs = append(genesis.Outputs, f.Genesis.Outputs...)
		genesis.Grants = append(genesis.Grants, f.Genesis.Grants...)
		accounts = append(accounts, apppkg.GenesisAccount{Owner: f.Org.Org, Asset: protocol.AssetCAL, Balance: f.Genesis.Grants[0].Amount}, apppkg.GenesisAccount{Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1000000000})
	}
	accounts = append(accounts, apppkg.GenesisAccount{Owner: protocol.ReserveFundingAccount(genesis.Network, genesis.Grants[0].Subject), Asset: protocol.AssetCAL, Balance: 1000})
	orgs := []protocol.OrgConfig{x.f[0].Org, x.f[1].Org}
	x.p, err = settings.Policy(x.f[0].Schedule, orgs)
	if err != nil {
		t.Fatal(err)
	}
	x.ledger = store.NewMemory()
	x.engine, err = apppkg.NewEngine(apppkg.EngineConfig{Network: genesis.Network, Organizations: orgs, Genesis: genesis, Schedule: x.f[0].Schedule, Direct: &settings, Accounts: accounts}, x.ledger)
	if err != nil {
		t.Fatal(err)
	}
	blockDB := dbm.NewMemDB()
	x.blocks = cmtstore.NewBlockStore(blockDB)
	if err = x.engine.EnableRepair(x.blocks); err != nil {
		t.Fatal(err)
	}
	x.app, err = apppkg.NewTimedApp(chain, x.ledger, x.engine.Check, x.engine.ExecuteAt, x.engine.BeginBlock)
	if err != nil {
		t.Fatal(err)
	}
	for j := range x.m {
		for i := range x.m[j] {
			x.db[j][i] = store.NewMemory()
			x.m[j][i], err = member.New(member.Config{Organization: x.f[j].Org, Index: uint16(i), Key: x.f[j].Keys[i], Peers: orgs, Schedule: x.f[j].Schedule, Workers: 1, Direct: &settings}, x.db[j][i], genesis)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() {
		for j := range x.db {
			for _, db := range x.db[j] {
				db.Close()
			}
		}
		x.ledger.Close()
		blockDB.Close()
	})
	x.baseline = x.money(t)
	return x
}

func (x *boundaryLab) tx(t *testing.T, org, index int, nonce uint64, ownerFee bool, feeIndex int, parent *protocol.DirectPayment, dest int) protocol.DirectRequest {
	t.Helper()
	f := x.f[org]
	tx, err := f.FastTransaction(index, nonce, x.p)
	if err != nil {
		t.Fatal(err)
	}
	b := tx.Body
	claims := tx.Claims
	var parents []protocol.InputCertificate
	if parent != nil {
		b.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: parent.Certificate.Summary.OutputID(0), Evidence: protocol.Hash(parent.Certificate.QC.Fact)}}
		claims = []protocol.InputClaim{{Output: parent.Tx.Body.Outputs[0]}}
		parents = []protocol.InputCertificate{{Certificate: parent.Certificate, Index: 0}}
	}
	b.Outputs[0].Recipient = protocol.NewDescriptor(f.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: x.f[dest].Org.Org}, f.Owner)
	if ownerFee {
		fee := x.fees[org][feeIndex]
		b.Fee = protocol.FeeTerms{Source: protocol.OwnerFinalUTXO, Maximum: 1000, Inputs: []protocol.Input{{Kind: protocol.FinalInput, Output: fee.ID, Evidence: fee.Fact}}, Refund: fee.Output.Recipient}
		b.Admission = nil
		for _, g := range f.Genesis.Grants {
			if g.Key.Kind != protocol.ResourceFUEL && g.Key.Kind != protocol.ResourcePolicy {
				b.Admission = append(b.Admission, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
			}
		}
	}
	b.Intent = b.IntentID()
	tx, err = protocol.NewFastTx(b, claims, x.p.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.Owner)}
	return protocol.DirectRequest{Tx: tx, InputCertificates: parents}
}

func (x *boundaryLab) approve(t *testing.T, org int, r protocol.DirectRequest, indices ...int) protocol.DirectPayment {
	t.Helper()
	p := protocol.DirectPayment{Tx: r.Tx, InputCertificates: r.InputCertificates}
	for _, i := range indices {
		v, err := x.m[org][i].ApproveDirect(context.Background(), r)
		if err != nil {
			t.Fatalf("approve org=%d member=%d: %v", org, i, err)
		}
		p.Certificate.Summary = v.Summary
		p.Certificate.QC.Fact = v.Summary.Fact()
		p.Certificate.QC.Votes = append(p.Certificate.QC.Votes, v.Vote)
	}
	if len(indices) >= 3 {
		if err := p.Certificate.Verify(x.f[org].Org); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func boundaryWire(t *testing.T, p protocol.DirectPayment) []byte {
	t.Helper()
	b, e := p.Submission().MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func (x *boundaryLab) append(t *testing.T, raw []byte) int64 {
	h := x.appendOne(t, raw)
	x.appendOne(t, nil) // The actual retained empty successor authenticates h.
	return h
}

func (x *boundaryLab) appendOne(t *testing.T, raw []byte, rejected ...bool) int64 {
	t.Helper()
	h := x.height + 1
	b := block(t, h, raw, x.last, x.vals)
	if raw == nil {
		b.Data = types.Data{}
		b.Header.DataHash = nil
	}
	b.Time = time.Unix(1700000000+40*h, 0).UTC()
	b.LastResultsHash = types.NewResults(x.lastResults).Hash()
	b.AppHash = x.appHash
	b.DataHash = b.Data.Hash()
	if err := b.ValidateBasic(); err != nil {
		t.Fatal(err)
	}
	parts, err := b.MakePartSet(types.BlockPartSizeBytes)
	if err != nil {
		t.Fatal(err)
	}
	id := types.BlockID{Hash: b.Hash(), PartSetHeader: parts.Header()}
	var txs [][]byte
	if raw != nil {
		txs = [][]byte{raw}
	}
	response, err := x.app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: h, Hash: b.Hash(), Time: b.Time, Txs: txs})
	if err != nil {
		t.Fatal(err)
	}
	if raw != nil {
		wantRejected := len(rejected) > 0 && rejected[0]
		if (response.TxResults[0].Code != 0) != wantRejected {
			t.Fatalf("public h=%d: %+v", h, response.TxResults[0])
		}
	}
	if _, err = x.app.Commit(context.Background(), &abci.RequestCommit{}); err != nil {
		t.Fatal(err)
	}
	x.appHash = response.AppHash
	x.last = commit(t, h, id, x.vals, x.keys)
	x.blocks.SaveBlock(b, parts, x.last)
	loaded := x.blocks.LoadBlock(h)
	if loaded == nil {
		t.Fatal("retained block missing")
	}
	if err := loaded.ValidateBasic(); err != nil {
		t.Fatal(err)
	}
	loadedParts, err := loaded.MakePartSet(types.BlockPartSizeBytes)
	if err != nil || !id.Equals(types.BlockID{Hash: loaded.Hash(), PartSetHeader: loadedParts.Header()}) {
		t.Fatal("retained complete identity", err)
	}
	if x.pending != nil {
		prev := x.blocks.LoadBlockMeta(h - 1).BlockID
		if !b.LastBlockID.Equals(prev) || !x.last.BlockID.Equals(x.blocks.LoadBlockMeta(h).BlockID) {
			t.Fatal("authenticated history does not match retained blocks")
		}
		proof, e := finality.VerifyBlock(finality.Trust{ChainID: chain, Network: x.f[0].Org.Network, Validators: x.vals}, finality.BlockData{Block: x.pending, Results: x.lastResults, Next: types.SignedHeader{Header: &b.Header, Commit: x.last}})
		if e != nil {
			t.Fatal(e)
		}
		for j := range x.m {
			for i, m := range x.m[j] {
				if err = blockfollow.Commit(x.db[j][i], proof, m.PrepareBlock); err != nil {
					t.Fatalf("follow org=%d member=%d h=%d: %v", j, i, h-1, err)
				}
			}
		}
	}
	x.pending = b
	x.lastResults = nil
	if raw != nil {
		x.lastResults = response.TxResults
	}
	x.height = h
	if got := x.money(t); got != x.baseline {
		t.Fatalf("money changed at h=%d: initial=%v got=%v", h, x.baseline, got)
	}
	return h
}

func boundaryLoad[T any](t *testing.T, db store.Store, key []byte) (value T) {
	t.Helper()
	if err := db.View(func(v state.ReadView) error { var e error; value, _, e = state.Load[T](v, key); return e }); err != nil {
		t.Fatal(err)
	}
	return
}

func boundaryRequire[T any](t *testing.T, db store.Store, key []byte) (value T) {
	t.Helper()
	if err := db.View(func(v state.ReadView) error {
		var found bool
		var e error
		value, found, e = state.Load[T](v, key)
		if e == nil && !found {
			return state.ErrNotFound
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	return
}

func boundaryRows(t *testing.T, db store.Store) []state.Entry {
	t.Helper()
	rows, e := store.Scan(db, nil, nil, 1000)
	if e != nil || len(rows) == 1000 {
		t.Fatal("fixture snapshot", e)
	}
	return rows
}

func (x *boundaryLab) quota(t *testing.T, i int, available, reserved uint64) {
	t.Helper()
	q, e := x.m[0][i].Quota(x.f[0].Genesis.Grants[0].Key, 0)
	if e != nil || q.Available != available || q.Reserved != reserved {
		t.Fatal("quota", q, e)
	}
}

// CAL projection subtracts open source gaps. FUEL includes all UTXOs, accounts,
// held escrow, rewards and burn. Frozen owner inputs remain assets, not spendable
// liquidity; snapshots below record their local locks separately.
func (x *boundaryLab) money(t *testing.T) (totals [2]int64) {
	t.Helper()
	rows, err := store.Scan(x.ledger, nil, nil, 1000)
	if err != nil || len(rows) == 1000 {
		t.Fatal("unbounded fixture", err)
	}
	var gap uint64
	coverage := make(map[protocol.ResourceKey]rules.PublicUsage)
	for _, r := range rows {
		if r.Key[2] == 102 {
			ob := boundaryLoad[rules.DirectObligation](t, x.ledger, r.Key)
			if ob.Status == rules.DirectOpen {
				gap += ob.Amount
			}
		}
		if r.Key[2] == 100 {
			c := boundaryLoad[struct {
				Summary protocol.OutputSummary
				Credit  rules.CoverageBalance
			}](t, x.ledger, r.Key)
			if c.Credit.Original != c.Credit.Paid+c.Credit.Discharged+c.Credit.Remaining || c.Credit.Recovered > c.Credit.Paid {
				t.Fatal("certificate coverage conservation")
			}
			for _, a := range c.Summary.Admission {
				if a.Key.Kind == protocol.ResourceCAL {
					u := coverage[a.Key]
					u.Reserved += c.Credit.Remaining
					u.Spent += c.Credit.Paid - c.Credit.Recovered
					coverage[a.Key] = u
				}
			}
		}
	}
	if gap != boundaryLoad[uint64](t, x.ledger, rules.DirectGapKey()) {
		t.Fatal("gap differs from independently summed obligations")
	}
	for _, r := range rows {
		switch r.Key[2] {
		case state.KeyCreation:
			c := boundaryLoad[state.Creation](t, x.ledger, r.Key)
			key := append([]byte(nil), r.Key...)
			key[2] = state.KeySpend
			s := boundaryLoad[state.Spend](t, x.ledger, key)
			if c.Final && s.Consumed == (protocol.SpendFactID{}) {
				totals[int(c.Output.Asset)-1] += int64(c.Output.Amount)
			}
		case state.KeyAccount:
			totals[int(r.Key[len(r.Key)-1])-1] += int64(boundaryLoad[uint64](t, x.ledger, r.Key))
		case state.KeyReward, state.KeyBurned:
			totals[1] += int64(boundaryLoad[uint64](t, x.ledger, r.Key))
		case 103:
			p := boundaryLoad[rules.DirectPaymentState](t, x.ledger, r.Key)
			if p.Fee.Held+p.Fee.Rewards+p.Fee.Burned+p.Fee.Refunded != p.Fee.Maximum {
				t.Fatal("fee conservation")
			}
			totals[1] += int64(p.Fee.Held)
		case state.KeyGrant:
			g := boundaryLoad[state.Grant](t, x.ledger, r.Key)
			if g.Key.Kind == protocol.ResourceCAL {
				u := boundaryLoad[rules.PublicUsage](t, x.ledger, state.Key(state.KeyUsage, g.Key.Encode()))
				if u != coverage[g.Key] {
					t.Fatal("grant usage differs from unique certificate coverage", u, coverage[g.Key])
				}
				bal := boundaryLoad[uint64](t, x.ledger, rules.AccountKey(g.Key.Account, protocol.AssetCAL))
				if u.Reserved+u.Spent > g.Amount || bal < g.Amount-u.Spent {
					t.Fatal("coverage/backing violated", g, u, bal)
				}
			}
		}
	}
	totals[0] -= int64(boundaryLoad[uint64](t, x.ledger, rules.DirectGapKey()))
	return
}

func (x *boundaryLab) snapshot(t *testing.T, label string) {
	t.Helper()
	v := map[string]any{"label": label, "height": x.height, "money": x.money(t)}
	for j := range x.f {
		v[fmt.Sprintf("org%d_CAL", j)] = boundaryLoad[uint64](t, x.ledger, rules.AccountKey(x.f[j].Org.Org, protocol.AssetCAL))
		v[fmt.Sprintf("org%d_FUEL", j)] = boundaryLoad[uint64](t, x.ledger, rules.AccountKey(x.f[j].Org.Org, protocol.AssetFUEL))
		v[fmt.Sprintf("org%d_usage", j)] = boundaryLoad[rules.PublicUsage](t, x.ledger, state.Key(state.KeyUsage, x.f[j].Genesis.Grants[0].Key.Encode()))
		var slices []state.Slice
		for _, m := range x.m[j] {
			q, e := m.Quota(x.f[j].Genesis.Grants[0].Key, 0)
			if e != nil {
				t.Fatal(e)
			}
			slices = append(slices, q)
		}
		v[fmt.Sprintf("org%d_member_CAL", j)] = slices
		local, err := store.Scan(x.db[j][0], nil, nil, 1000)
		if err != nil || len(local) == 1000 {
			t.Fatal(err)
		}
		var locks []map[string]any
		for _, r := range local {
			if r.Key[2] == state.KeySpend || r.Key[2] == 122 {
				locks = append(locks, map[string]any{"key": fmt.Sprintf("%x", r.Key), "value": json.RawMessage(r.Value)})
			}
		}
		v[fmt.Sprintf("org%d_member0_locks_progress", j)] = locks
	}
	rows, err := store.Scan(x.ledger, nil, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	for _, r := range rows {
		if r.Key[2] == 100 || r.Key[2] == 101 || r.Key[2] == 102 || r.Key[2] == 103 {
			records = append(records, map[string]any{"key": fmt.Sprintf("%x", r.Key), "value": json.RawMessage(r.Value)})
		}
	}
	v["public_records"] = records
	v["ledger_rows"] = rows
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	t.Log("BOUNDARY", string(b))
}

func (x *boundaryLab) increase(t *testing.T, delta uint64) {
	t.Helper()
	g := boundaryLoad[state.Grant](t, x.ledger, state.Key(state.KeyGrant, x.f[0].Genesis.Grants[0].Key.Encode()))
	c := protocol.ReserveIncrease{Network: x.f[0].Org.Network, Organization: x.f[0].Org.Hash(), Key: g.Key, Grant: g.ID, Previous: g.Amount, Amount: delta}
	c.Sign(x.f[0].Owner)
	b, e := c.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	source := rules.AccountKey(protocol.ReserveFundingAccount(x.f[0].Org.Network, g.Subject), protocol.AssetCAL)
	target := rules.AccountKey(x.f[0].Org.Org, protocol.AssetCAL)
	beforeSource := boundaryRequire[uint64](t, x.ledger, source)
	beforeTarget := boundaryRequire[uint64](t, x.ledger, target)
	x.append(t, b)
	if boundaryRequire[uint64](t, x.ledger, source) != beforeSource-delta || boundaryRequire[uint64](t, x.ledger, target) != beforeTarget+delta {
		t.Fatal("funding conservation")
	}
	if boundaryRequire[state.Grant](t, x.ledger, state.Key(state.KeyGrant, g.Key.Encode())).Amount != g.Amount+delta {
		t.Fatal("funding grant")
	}
}

func TestRevisionCapacityFragmentationAndFunding(t *testing.T) {
	x := newBoundaryLab(t)
	reqs := make([]protocol.DirectRequest, 4)
	facts := make([]protocol.SpendFactID, 4)
	inputs, fees, intents := map[protocol.OutputID]bool{}, map[protocol.OutputID]bool{}, map[protocol.Hash]bool{}
	for i := range reqs {
		reqs[i] = x.tx(t, 0, i, uint64(i+1), true, i, nil, 0)
		p := x.approve(t, 0, reqs[i], i, (i+3)%4)
		facts[i] = p.Certificate.QC.Fact
		if len(p.Certificate.QC.Votes) != 2 {
			t.Fatal("expected exactly two approvals")
		}
		r := reqs[i]
		in, fee, intent := r.Tx.Body.Inputs[0].Output, r.Tx.Body.Fee.Inputs[0].Output, protocol.Hash(r.Tx.Body.Intent)
		if inputs[in] || fees[fee] || intents[intent] {
			t.Fatal("fixture requests overlap")
		}
		inputs[in] = true
		fees[fee] = true
		intents[intent] = true
	}
	x.snapshot(t, "four_independent_requests_two_votes_each")
	for i := range x.m[0] {
		x.quota(t, i, 0, 200)
	}
	for i, r := range reqs {
		for m := 0; m < 4; m++ {
			if m == i || m == (i+3)%4 {
				continue
			}
			if _, e := x.m[0][m].ApproveDirect(context.Background(), r); !errors.Is(e, rules.ErrLimited) {
				t.Fatalf("expected capacity refusal tx%d m%d: %v", i, m, e)
			}
		}
	}
	// A fixed idle window has no block or new-request event. Its observation
	// is separate from the rule argument that elapsed wall time cannot release.
	beforeIdle := boundaryRows(t, x.db[0][0])
	time.Sleep(time.Second)
	if !reflect.DeepEqual(beforeIdle, boundaryRows(t, x.db[0][0])) {
		t.Fatal("idle changed approval state")
	}
	x.increase(t, 150)
	for i, r := range reqs {
		for _, m := range []int{i, (i + 3) % 4} {
			if boundaryRequire[state.Spend](t, x.db[0][m], rules.DirectSpendKey(r.Tx.Body.Inputs[0].Output, 0)).Candidate != facts[i] {
				t.Fatal("funding removed original lock")
			}
		}
	}
	x.snapshot(t, "funded_150_no_locks_deleted")
	for i := range x.m[0] {
		x.quota(t, i, 100, 200)
	}
	for _, r := range reqs {
		p := x.approve(t, 0, r, 0, 1, 2)
		x.append(t, boundaryWire(t, p))
	}
	for _, m := range x.m[0] {
		q, e := m.Quota(x.f[0].Genesis.Grants[0].Key, 0)
		if e != nil || q.Reserved != 0 || q.Available != 300 {
			t.Fatal("capacity did not circulate", q, e)
		}
	}
	x.snapshot(t, "all_four_closed")
}

func TestRevisionSplitInputNotUnlockedByFunding(t *testing.T) {
	x := newBoundaryLab(t)
	a := x.tx(t, 0, 0, 1, true, 0, nil, 0)
	b := x.tx(t, 0, 0, 2, true, 1, nil, 0)
	x.approve(t, 0, a, 0, 1)
	x.approve(t, 0, b, 2, 3)
	for _, phase := range []string{"before_funding", "after_funding"} {
		if phase == "after_funding" {
			x.increase(t, 150)
		}
		for i := 0; i < 4; i++ {
			r := a
			if i < 2 {
				r = b
			}
			if _, e := x.m[0][i].ApproveDirect(context.Background(), r); !errors.Is(e, rules.ErrConflict) {
				t.Fatalf("input lock lost: %v", e)
			}
		}
		available := uint64(100)
		reserve := uint64(300)
		if phase == "after_funding" {
			available = 200
			reserve = 450
		}
		for i := 0; i < 4; i++ {
			x.quota(t, i, available, 100)
		}
		if boundaryRequire[uint64](t, x.ledger, rules.AccountKey(x.f[0].Org.Org, protocol.AssetCAL)) != reserve {
			t.Fatal("split spent reserve")
		}
		if u := boundaryLoad[rules.PublicUsage](t, x.ledger, state.Key(state.KeyUsage, x.f[0].Genesis.Grants[0].Key.Encode())); u.Reserved != 0 || u.Spent != 0 {
			t.Fatal("split public exposure", u)
		}
		x.snapshot(t, phase)
	}
}

func TestRevisionIntentTwoRoundCompensation(t *testing.T) {
	for _, owner := range []bool{true, false} {
		t.Run(fmt.Sprintf("owner_fee_%t", owner), func(t *testing.T) {
			x := newBoundaryLab(t)
			var returned *protocol.DirectPayment
			for round := 0; round < 2; round++ {
				nonce := uint64(100 + round)
				losingReq := x.tx(t, 0, 0, nonce, owner, round, returned, 1)
				loser := x.approve(t, 0, losingReq, 0, 1, 2)
				winner := x.approve(t, 1, x.tx(t, 1, round, nonce, owner, round, nil, 1), 0, 1, 2)
				if loser.Tx.Body.Intent != winner.Tx.Body.Intent {
					t.Fatal("fixture must collide in global Intent")
				}
				x.append(t, boundaryWire(t, winner))
				raw := boundaryWire(t, loser)
				before := boundaryRows(t, x.ledger)
				err := x.ledger.Update(func(v state.ReadView) ([]state.Change, error) {
					tr, e := x.engine.ExecuteAt(v, raw, apppkg.BlockContext{Height: x.height + 1, Time: time.Unix(1700000000+40*(x.height+1), 0)})
					return tr.Changes, e
				})
				after := boundaryRows(t, x.ledger)
				if !errors.Is(err, rules.ErrConflict) || !reflect.DeepEqual(before, after) {
					t.Fatal("Intent loser affected public state", err)
				}
				// Include the rejected command and authenticated failure result in the
				// actual retained history; following it must preserve local locks/credit.
				var localBefore [4]member.LocalProgress
				for i := range localBefore {
					localBefore[i] = boundaryLoad[member.LocalProgress](t, x.db[0][i], member.ProgressKey(loser.Certificate.QC.Fact))
				}
				x.appendOne(t, raw, true)
				x.appendOne(t, nil)
				for i := range localBefore {
					if got := boundaryLoad[member.LocalProgress](t, x.db[0][i], member.ProgressKey(loser.Certificate.QC.Fact)); !reflect.DeepEqual(got, localBefore[i]) {
						t.Fatal("failed result changed progress")
					}
					if i < 3 {
						x.quota(t, i, 200-uint64(round+1)*100, uint64(round+1)*100)
					}
				}
				dest := 0
				if round == 1 {
					dest = 1
				}
				child := x.approve(t, 1, x.tx(t, 1, 0, uint64(200+round), owner, round+3, &loser, dest), 0, 1, 2)
				h := x.append(t, boundaryWire(t, child))
				out := loser.Certificate.Summary.OutputID(0)
				x.append(t, protocol.ClockTick(x.f[0].Org.Network, x.height+1))
				x.snapshot(t, fmt.Sprintf("round%d_before_compensation", round+1))
				decision := protocol.CompensationDecision{Network: x.f[0].Org.Network, Output: out, Height: h, Transaction: 0, Input: 0}
				d, e := decision.MarshalBinary()
				if e != nil {
					t.Fatal(e)
				}
				x.append(t, d)
				if err := x.ledger.View(func(v state.ReadView) error {
					tr, e := x.engine.ExecuteAt(v, d, apppkg.BlockContext{Height: x.height + 1, Time: time.Unix(1700000000+40*(x.height+1), 0)})
					if e != nil {
						return e
					}
					result, effects, e := protocol.DecodePublicExecution(x.f[0].Org.Network, d, tr.Data)
					if e != nil || len(tr.Changes) != 0 || result.Applied || len(effects) != 0 || len(result.FeeOutputs) != 0 {
						t.Fatal("duplicate compensation effects", e)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				x.append(t, d)
				bal := boundaryLoad[uint64](t, x.ledger, rules.AccountKey(x.f[0].Org.Org, protocol.AssetCAL))
				if bal != 300-uint64(round+1)*100 {
					t.Fatal("wrong net reserve loss", bal)
				}
				spend := boundaryLoad[state.Spend](t, x.ledger, rules.DirectSpendKey(loser.Tx.Body.Inputs[0].Output, 0))
				creation := boundaryRequire[state.Creation](t, x.ledger, rules.DirectCreationKey(loser.Tx.Body.Inputs[0].Output, 0))
				if !creation.Final {
					t.Fatal("original principal not final")
				}
				if spend.Consumed != (protocol.SpendFactID{}) {
					t.Fatal("rejected source input consumed")
				}
				for i := 0; i < 3; i++ {
					x.quota(t, i, 200-uint64(round+1)*100, uint64(round+1)*100)
					p := boundaryRequire[member.LocalProgress](t, x.db[0][i], member.ProgressKey(loser.Certificate.QC.Fact))
					if p.Settled || p.Paid != 100 || p.Recovered != 0 {
						t.Fatal("loser progress", p)
					}
					local := boundaryLoad[state.Spend](t, x.db[0][i], rules.DirectSpendKey(loser.Tx.Body.Inputs[0].Output, 0))
					if local.Candidate != loser.Certificate.QC.Fact {
						t.Fatal("original source lock disappeared")
					}
				}
				x.quota(t, 3, 200, 0)
				u := boundaryRequire[rules.PublicUsage](t, x.ledger, state.Key(state.KeyUsage, x.f[0].Genesis.Grants[0].Key.Encode()))
				if u.Reserved != 0 || u.Spent != uint64(round+1)*100 {
					t.Fatal("net public usage", u)
				}
				if owner {
					fee := loser.Tx.Body.Fee.Inputs[0]
					c := boundaryRequire[state.Creation](t, x.ledger, rules.DirectCreationKey(fee.Output, 0))
					s := boundaryLoad[state.Spend](t, x.ledger, rules.DirectSpendKey(fee.Output, 0))
					if !c.Final || c.Output.Amount != 10000 || s.Consumed != (protocol.SpendFactID{}) {
						t.Fatal("locked fee principal", c, s)
					}
					for i := 0; i < 3; i++ {
						if boundaryRequire[state.Spend](t, x.db[0][i], rules.DirectSpendKey(fee.Output, 0)).Candidate != loser.Certificate.QC.Fact {
							t.Fatal("fee lock lost")
						}
					}
				}
				x.snapshot(t, fmt.Sprintf("round%d_paid_duplicate_noop", round+1))
				returned = &child
			}
			// The first three members have 200 reserved and cannot issue a third QC;
			// this slice stoppage happens before the 300-CAL reserve is empty.
			next := x.tx(t, 0, 4, 999, owner, 8, nil, 1)
			for i := 0; i < 3; i++ {
				if _, e := x.m[0][i].ApproveDirect(context.Background(), next); !errors.Is(e, rules.ErrLimited) {
					t.Fatal("expected exhausted signer slice", e)
				}
			}
			// The final compensation-funded descendant is actually spendable at B.
			usable := x.approve(t, 1, x.tx(t, 1, 0, 1000, owner, 9, returned, 1), 0, 1, 2)
			x.append(t, boundaryWire(t, usable))
			x.snapshot(t, "two_round_stop_original_signers")
		})
	}
}

func TestRevisionUnauthorizedSponsorAndLegacyEntry(t *testing.T) {
	x := newBoundaryLab(t, true)
	r := x.tx(t, 0, 0, 1, false, 0, nil, 0)
	for i, m := range x.m[0] {
		before := boundaryRows(t, x.db[0][i])
		if _, err := m.ApproveDirect(context.Background(), r); !errors.Is(err, protocol.ErrAuth) {
			t.Fatal("unauthorized sponsorship accepted", err)
		}
		after := boundaryRows(t, x.db[0][i])
		if !reflect.DeepEqual(before, after) {
			t.Fatal("rejection changed member state")
		}
	}
	// A cryptographically valid test-key QC does not override the public policy
	// guard. This negative probe is explicitly not a reachable honest quorum.
	c, err := x.f[0].DirectCertificate(r.Tx, x.p)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Verify(x.f[0].Org); err != nil {
		t.Fatal("negative probe needs valid QC", err)
	}
	wire := boundaryWire(t, protocol.DirectPayment{Tx: r.Tx, Certificate: c})
	err = x.ledger.View(func(v state.ReadView) error {
		tr, e := x.engine.ExecuteAt(v, wire, apppkg.BlockContext{Height: 1, Time: time.Unix(1700000040, 0)})
		if len(tr.Changes) != 0 {
			t.Fatal("unauthorized execution changed state")
		}
		return e
	})
	if !errors.Is(err, protocol.ErrAuth) {
		t.Fatal("public policy bypass", err)
	}
	legacy, err := x.f[0].Transaction(0, 2).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if x.engine.Check(legacy) == nil {
		t.Fatal("wire4 accepted legacy envelope")
	}
	err = x.ledger.View(func(v state.ReadView) error {
		tr, e := x.engine.ExecuteAt(v, legacy, apppkg.BlockContext{Height: 1, Time: time.Unix(1700000040, 0)})
		if len(tr.Changes) != 0 {
			t.Fatal("legacy entry wrote state")
		}
		return e
	})
	if err == nil {
		t.Fatal("legacy bypass")
	}
	// Same payer and principal succeed when the payer supplies FUEL.
	good := x.approve(t, 0, x.tx(t, 0, 0, 3, true, 0, nil, 0), 0, 1, 2)
	for _, count := range []int{0, 2} {
		bad := good
		bad.Certificate.QC.Votes = bad.Certificate.QC.Votes[:count]
		raw, encodeErr := bad.Submission().MarshalBinary()
		if encodeErr != nil {
			if !errors.Is(encodeErr, protocol.ErrAuth) {
				t.Fatal(encodeErr)
			}
			continue // The canonical wire encoder already enforces this boundary.
		}
		if err := x.ledger.View(func(v state.ReadView) error {
			tr, e := x.engine.ExecuteAt(v, raw, apppkg.BlockContext{Height: 1, Time: time.Unix(1700000040, 0)})
			if e == nil || len(tr.Changes) != 0 {
				t.Fatal("insufficient current authorization bypass", count, e)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	x.append(t, boundaryWire(t, good))
}
