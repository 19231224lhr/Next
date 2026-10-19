package committee_test

import (
	"bytes"
	abci "github.com/cometbft/cometbft/abci/types"
	"path/filepath"
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

func TestFinalC3EpochCreditBeforeFirstTopupReplay(t *testing.T) {
	f, c := c3EngineConfig(t)
	other := testkit.NewFixture("c3-engine", "payer-org", 1)
	settings := c.Direct.C3
	settings.Policies[0].Version = 2
	settings.Policies[0].Schedule = protocol.FeeSchedule{AttachReward: 1, ResolveReward: 1, Compensate: 4, Repay: 5}
	settings.EpochBlocks = 1
	settings.ReservedBPS = 2500
	settings.Committee = []rules.C3Validator{{ID: protocol.Digest("validator"), Address: bytes.Repeat([]byte{1}, 20), Weight: 1}}
	otherPool := protocol.Digest("payer-pool")
	otherGrant := state.Grant{ID: protocol.Digest("payer-grant"), Organization: other.Org.Hash(), Key: protocol.ResourceKey{Kind: protocol.ResourceFUEL, Account: otherPool, Version: 1}, Amount: 100, Subject: other.Genesis.Outputs[0].Output.Recipient.Owner}
	settings.Issuers = append(settings.Issuers, rules.C3Issuer{Config: other.Org.Hash(), Funding: protocol.AdmissionRef{Key: otherGrant.Key, Grant: otherGrant.ID}, Revenue: protocol.Digest("payer-revenue"), Beneficiary: protocol.Digest("payer-beneficiary")})
	c.Organizations = append(c.Organizations, other.Org)
	c.Genesis.Grants = append(c.Genesis.Grants, otherGrant)
	c.Accounts = append(c.Accounts, committee.GenesisAccount{Owner: other.Org.Org, Asset: protocol.AssetCAL, Balance: 100}, committee.GenesisAccount{Owner: otherPool, Asset: protocol.AssetFUEL, Balance: 100})
	ledgerPath := filepath.Join(t.TempDir(), "ledger.db")
	ledgerID := store.Identity{Network: "credit-replay", Role: "committee", Node: "0", Schema: 9}
	ledger, e := store.Open(ledgerPath, ledgerID)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { ledger.Close() }()
	engine, e := committee.NewEngine(c, ledger)
	if e != nil {
		t.Fatal(e)
	}
	// Start from an accounted timeout-pool fixture: payer has spent 9 and its
	// protected free balance is 91. Epoch transitions and top-ups use real engine
	// execution; the recipient follows only consensus-verified public blocks.
	if e = ledger.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if e := state.Put(o, rules.AccountKey(otherPool, protocol.AssetFUEL), uint64(91)); e != nil {
			return nil, e
		}
		if e := state.Put(o, state.Key(state.KeyUsage, otherGrant.Key.Encode()), rules.PublicUsage{Spent: 9}); e != nil {
			return nil, e
		}
		if e := state.Put(o, rules.C3EpochKey(), rules.C3EpochState{Pool: []rules.C3PoolPart{{Payer: other.Org.Org, Amount: 9}}}); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	}); e != nil {
		t.Fatal(e)
	}
	memberPath := filepath.Join(t.TempDir(), "member.db")
	memberID := store.Identity{Network: "credit-replay", Role: "member", Node: "0", Schema: 9}
	md, e := store.Open(memberPath, memberID)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { md.Close() }()
	mc := member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Peers: c.Organizations, Schedule: f.Schedule, Workers: 1, Direct: c.Direct, DisableSourceRecovery: true}
	m, e := member.New(mc, md, c.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	var previous []byte
	block := func(h int64, txs [][]byte) finality.VerifiedBlock {
		t.Helper()
		var results []*abci.ExecTxResult
		if e := ledger.Update(func(v state.ReadView) ([]state.Change, error) {
			begin, e := engine.BeginBlock(v, committee.BlockContext{Height: h, Time: time.Unix(100+h, 0)})
			if e != nil {
				return nil, e
			}
			o := state.NewOverlay(v)
			o.Apply(begin.Changes)
			for i, raw := range txs {
				tr, e := engine.ExecuteAt(o, raw, committee.BlockContext{Height: h, Index: uint32(i), Time: time.Unix(100+h, 0), ProposerAddress: settings.Committee[0].Address})
				if e != nil {
					return nil, e
				}
				o.Apply(tr.Changes)
				results = append(results, &abci.ExecTxResult{Data: tr.Data})
			}
			return o.Changes(), nil
		}); e != nil {
			t.Fatal(e)
		}
		trust, data := testkit.Block("c3-engine", h, previous, txs, results)
		b, e := finality.VerifyBlock(trust, data)
		if e != nil {
			t.Fatal(e)
		}
		previous = b.Hash()
		return b
	}
	first := block(1, nil)
	if e = blockfollow.Commit(md, first, m.PrepareBlock); e != nil {
		t.Fatal(e)
	}
	if e = md.Close(); e != nil {
		t.Fatal(e)
	}
	md, e = store.Open(memberPath, memberID)
	if e != nil {
		t.Fatal(e)
	}
	m, e = member.New(mc, md, c.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	if e = ledger.Close(); e != nil {
		t.Fatal(e)
	}
	ledger, e = store.Open(ledgerPath, ledgerID)
	if e != nil {
		t.Fatal(e)
	}
	engine, e = committee.NewEngine(c, ledger)
	if e != nil {
		t.Fatal(e)
	}
	issuer := settings.Issuers[0]
	topup := protocol.ReserveIncrease{Network: c.Network, Organization: f.Org.Hash(), Grant: issuer.Funding.Grant, Key: issuer.Funding.Key, Previous: 109, Amount: 10}
	topup.Sign(f.Owner)
	raw, e := topup.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	next := topup
	next.Previous = 119
	next.Amount = 3
	next.Sign(f.Owner)
	nextRaw, e := next.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	second := block(2, [][]byte{raw, raw, nextRaw})
	if e = blockfollow.Commit(md, second, m.PrepareBlock); e != nil {
		t.Fatal("epoch credit was not applied before first top-up", e)
	}
	if e = blockfollow.Commit(md, second, m.PrepareBlock); e != nil {
		t.Fatal(e)
	}
	if e = md.Close(); e != nil {
		t.Fatal(e)
	}
	md, e = store.Open(memberPath, memberID)
	if e != nil {
		t.Fatal(e)
	}
	m, e = member.New(mc, md, c.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	if e = blockfollow.Commit(md, second, m.PrepareBlock); e != nil {
		t.Fatal(e)
	}
	if e = md.View(func(v state.ReadView) error {
		g, _, e := state.Load[state.Grant](v, state.Key(state.KeyGrant, issuer.Funding.Key.Encode()))
		if g.Amount != 122 {
			t.Fatalf("reflow/top-up replay amount %d", g.Amount)
		}
		slice, _, e := state.Load[state.Slice](v, state.SliceKey(issuer.Funding.Key, 0))
		if slice.Available != 81 || slice.Reserved != 0 {
			t.Fatalf("replay slice %+v", slice)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
