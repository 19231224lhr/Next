package committee

import (
	"context"
	"encoding/json"
	"fmt"
	abci "github.com/cometbft/cometbft/abci/types"
	"testing"
	"time"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestReserveSharedExternalFunding(t *testing.T) {
	cfg, f, _, _ := cacheFixture(t, 1)
	var first state.Grant
	for _, g := range cfg.Genesis.Grants {
		if g.Key.Kind == protocol.ResourceCAL {
			first = g
		}
	}
	other := testkit.NewFixture("verify-cache", "second", 1).Org
	second := first
	second.ID = protocol.Digest("second-reserve-grant", nil)
	second.Organization = other.Hash()
	second.Key.Account = other.Org
	second.Amount = 1000
	cfg.Organizations = append(cfg.Organizations, other)
	cfg.Genesis.Grants = append(cfg.Genesis.Grants, second)
	sourceID := protocol.ReserveFundingAccount(cfg.Network, first.Subject)
	cfg.Accounts = append(cfg.Accounts,
		GenesisAccount{Owner: sourceID, Asset: protocol.AssetCAL, Balance: 500},
		GenesisAccount{Owner: other.Org, Asset: protocol.AssetCAL, Balance: 1000})
	db := store.NewMemory()
	defer db.Close()
	engine, err := NewEngine(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewEngine(cfg, db); err != nil {
		t.Fatal("valid reopen", err)
	}
	command := func(g state.Grant, amount uint64) []byte {
		c := protocol.ReserveIncrease{Network: cfg.Network, Organization: g.Organization, Key: g.Key, Grant: g.ID, Previous: g.Amount, Amount: amount}
		c.Sign(f.Owner)
		raw, err := c.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	a, b := command(first, 100), command(second, 300)
	second.Amount += 300
	tooMuch := command(second, 200)
	// Execute both top-ups, a replay and an overdraw in one block overlay.
	if err = db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		for i, raw := range [][]byte{a, a, b, tooMuch} {
			tr, err := engine.ExecuteAt(o, raw, BlockContext{Height: 1, Time: time.Unix(100, 0), Index: uint32(i)})
			if i == 3 {
				if err == nil || len(tr.Changes) != 0 {
					t.Fatal("overdraw must reject without changes")
				}
				continue
			}
			if err != nil {
				return nil, err
			}
			if i == 1 && len(tr.Changes) != 0 {
				t.Fatal("replay changed state")
			}
			o.Apply(tr.Changes)
		}
		return o.Changes(), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = db.View(func(v state.ReadView) error {
		for owner, want := range map[protocol.Hash]uint64{sourceID: 100, f.Org.Org: 1000000100, other.Org: 1300} {
			got, found, err := state.Load[uint64](v, rules.AccountKey(owner, protocol.AssetCAL))
			if err != nil {
				return err
			}
			if !found || got != want {
				t.Fatalf("balance %d, want %d", got, want)
			}
		}
		first.Amount += 100
		for _, want := range []state.Grant{first, second} {
			got, _, err := state.Load[state.Grant](v, state.Key(state.KeyGrant, want.Key.Encode()))
			if err != nil {
				return err
			}
			if got != want {
				t.Fatal("incorrect grant", got, want)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReserveFundingRejectsProtectedAccounts(t *testing.T) {
	for _, mode := range []string{"same_account", "other_org", "other_grant"} {
		for _, reopen := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reopen=%t", mode, reopen), func(t *testing.T) {
				cfg, f, _, _ := cacheFixture(t, 1)
				c := protocol.ReserveIncrease{}
				c.Sign(f.Owner)
				source := protocol.ReserveFundingAccount(cfg.Network, c.Subject)
				org := f.Org
				if mode == "same_account" {
					org.Org = source
				}
				cfg.Organizations = []protocol.OrgConfig{org}
				g := state.Grant{ID: protocol.Digest("reserve-test-grant", nil), Organization: org.Hash(), Key: protocol.ResourceKey{Kind: protocol.ResourceCAL, Account: org.Org, Version: 1}, Amount: 1000, Subject: c.Subject}
				cfg.Genesis.Outputs = nil
				cfg.Genesis.Grants = []state.Grant{g}
				cfg.Accounts = []GenesisAccount{{Owner: org.Org, Asset: protocol.AssetCAL, Balance: 1000}}
				if mode != "same_account" {
					cfg.Accounts = append(cfg.Accounts, GenesisAccount{Owner: source, Asset: protocol.AssetCAL, Balance: 1000})
				}
				if mode == "other_org" {
					other := testkit.NewFixture("verify-cache", "other", 1).Org
					other.Org = source
					cfg.Organizations = append(cfg.Organizations, other)
				}
				if mode == "other_grant" {
					other := g
					other.ID = protocol.Digest("reserve-test-other-grant", nil)
					other.Key.Account = source
					cfg.Genesis.Grants = append(cfg.Genesis.Grants, other)
				}
				db := store.NewMemory()
				defer db.Close()
				if reopen {
					// Simulate a matching genesis accepted by the old implementation.
					b, err := json.Marshal(cfg)
					if err != nil {
						t.Fatal(err)
					}
					if err = db.Update(func(v state.ReadView) ([]state.Change, error) {
						o := state.NewOverlay(v)
						err := state.Put(o, state.Key(state.KeyGenesis), protocol.Digest("PUBLIC_GENESIS_V3", b))
						return o.Changes(), err
					}); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := NewEngine(cfg, db); err == nil {
					t.Fatal("protected reserve accepted as funding source")
				}
			})
		}
	}
}

func TestReserveCommandThroughVerifiedBlock(t *testing.T) {
	cfg, f, _, _ := cacheFixture(t, 1)
	var g state.Grant
	for _, x := range cfg.Genesis.Grants {
		if x.Key.Kind == protocol.ResourceCAL {
			g = x
		}
	}
	c := protocol.ReserveIncrease{Network: cfg.Network, Organization: g.Organization, Key: g.Key, Grant: g.ID, Previous: g.Amount, Amount: 300}
	c.Sign(f.Owner)
	cfg.Accounts = append(cfg.Accounts, GenesisAccount{Owner: protocol.ReserveFundingAccount(c.Network, c.Subject), Asset: protocol.AssetCAL, Balance: 300})
	db := store.NewMemory()
	defer db.Close()
	engine, err := NewEngine(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.Check(raw); err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, e := engine.ExecuteAt(v, raw, BlockContext{Height: 1, Time: time.Now()})
		return tr.Changes, e
	})
	if err != nil {
		t.Fatal(err)
	}
	local := store.NewMemory()
	defer local.Close()
	m, err := member.New(member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Schedule: f.Schedule, Workers: 1, Direct: cfg.Direct}, local, cfg.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	trust, data := testkit.Block("verify-cache", 1, nil, [][]byte{raw}, []*abci.ExecTxResult{{Code: 0}})
	b, err := finality.VerifyBlock(trust, data)
	if err != nil {
		t.Fatal(err)
	}
	if err = blockfollow.Commit(local, b, m.PrepareBlock); err != nil {
		t.Fatal(err)
	}
	if err = blockfollow.Commit(local, b, m.PrepareBlock); err != nil {
		t.Fatal(err)
	}
	if err = local.View(func(v state.ReadView) error {
		s, _, e := state.Load[state.Slice](v, state.SliceKey(g.Key, 0))
		share, _ := protocol.GrantShare(g.Amount + 300)
		if s.Available != share {
			t.Fatalf("member capacity %d expected %d", s.Available, share)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReserveLetsIdenticalWaitingRequestProceed(t *testing.T) {
	cfg, f, p, _ := cacheFixture(t, 2)
	var g state.Grant
	for i := range cfg.Genesis.Grants {
		if cfg.Genesis.Grants[i].Key.Kind == protocol.ResourceCAL {
			cfg.Genesis.Grants[i].Amount = 150
			g = cfg.Genesis.Grants[i]
		}
	}
	local := store.NewMemory()
	defer local.Close()
	m, err := member.New(member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Schedule: f.Schedule, Workers: 1, Direct: cfg.Direct}, local, cfg.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	tx1, err := f.FastTransaction(0, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	tx2, err := f.FastTransaction(1, 2, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx1}); err != nil {
		t.Fatal(err)
	}
	raw, err := (protocol.DirectRequest{Tx: tx2}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApproveDirectBytes(context.Background(), raw); err == nil {
		t.Fatal("exhausted capacity accepted")
	}
	c := protocol.ReserveIncrease{Network: cfg.Network, Organization: g.Organization, Key: g.Key, Grant: g.ID, Previous: 150, Amount: 150}
	c.Sign(f.Owner)
	command, err := c.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	trust, data := testkit.Block("verify-cache", 1, nil, [][]byte{command}, []*abci.ExecTxResult{{Code: 0}})
	b, err := finality.VerifyBlock(trust, data)
	if err != nil {
		t.Fatal(err)
	}
	if err = blockfollow.Commit(local, b, m.PrepareBlock); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApproveDirectBytes(context.Background(), raw); err != nil {
		t.Fatal("identical old request did not resume", err)
	}
	local.View(func(v state.ReadView) error {
		s, _, e := state.Load[state.Slice](v, state.SliceKey(g.Key, 0))
		if s.Reserved != 200 || s.Available != 0 {
			t.Fatal("old occupancy was reset", s)
		}
		return e
	})
}
