package committee

import (
	"context"
	abci "github.com/cometbft/cometbft/abci/types"
	"testing"
	"time"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/member"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

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
