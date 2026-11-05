package committee_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"utxo/internal/committee"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestDependencyDrainResumesAfterReopen(t *testing.T) {
	const chainID = "idle-reopen"
	f := testkit.NewFixture(chainID, "a", 1)
	root, err := f.Certify(f.Transaction(0, 1))
	if err != nil {
		t.Fatal(err)
	}
	chain := []protocol.TXCer{root}
	for i := 1; i <= committee.MaxDependencyActions+8; i++ {
		parent := chain[len(chain)-1]
		body := f.Transaction(0, uint64(i+1)).Body
		body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: parent.Effects.Outputs[0], Evidence: protocol.Hash(parent.QC.Fact)}}
		child, err := f.Certify(f.Sign(body), parent)
		if err != nil {
			t.Fatal(err)
		}
		chain = append(chain, child)
	}
	path := filepath.Join(t.TempDir(), "committee.db")
	identity := store.Identity{Network: chainID, Role: "committee", Node: "0", Schema: 2}
	db, err := store.Open(path, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	config := committee.EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Accounts: []committee.GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1_000_000_000}}}
	openApp := func() (*committee.Engine, *committee.App) {
		engine, err := committee.NewEngine(config, db)
		if err != nil {
			t.Fatal(err)
		}
		app, err := committee.NewApp(chainID, db, engine.Check, engine.Execute, engine.Drain)
		if err != nil {
			t.Fatal(err)
		}
		return engine, app
	}
	engine, app := openApp()
	commit := func(h int64, commands [][]byte) []byte {
		r, err := app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: h, Hash: bytes.Repeat([]byte{byte(h)}, 32), Txs: commands})
		if err != nil {
			t.Fatal(err)
		}
		for _, result := range r.TxResults {
			if result.Code != 0 {
				t.Fatalf("command failed: %+v", result)
			}
		}
		if _, err = app.Commit(context.Background(), &abci.RequestCommit{}); err != nil {
			t.Fatal(err)
		}
		return r.AppHash
	}
	var commands [][]byte
	for i := len(chain) - 1; i >= 0; i-- {
		raw, err := chain[i].MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		commands = append(commands, raw)
	}
	firstHash := commit(1, commands)
	settled := 0
	for _, c := range chain {
		p, err := engine.Payment(c.QC.Fact)
		if err != nil {
			t.Fatal(err)
		}
		if p.Settled {
			settled++
		}
	}
	if settled != committee.MaxDependencyActions+1 {
		t.Fatalf("need a partially drained durable queue, settled=%d", settled)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path, identity)
	if err != nil {
		t.Fatal(err)
	}
	engine, app = openApp()
	info, err := app.Info(context.Background(), &abci.RequestInfo{})
	if err != nil || info.LastBlockHeight != 1 || !bytes.Equal(info.LastBlockAppHash, firstHash) {
		t.Fatalf("reopen lost committed progress: %+v %v", info, err)
	}
	secondHash := commit(2, nil)
	if bytes.Equal(firstHash, secondHash) {
		t.Fatal("remaining maintenance did not advance AppHash")
	}
	for _, c := range chain {
		p, err := engine.Payment(c.QC.Fact)
		if err != nil || !p.Settled || !p.Fee.Closed {
			t.Fatalf("tail failed to settle after reopen: %+v %v", p, err)
		}
	}
	thirdHash := commit(3, nil)
	if !bytes.Equal(secondHash, thirdHash) {
		t.Fatal("empty maintenance keeps changing AppHash")
	}
	balance, err := engine.Account(f.Org.Org, protocol.AssetFUEL)
	if err != nil || balance != 1_000_000_000-uint64(len(chain))*94 {
		t.Fatalf("fee charged twice: balance=%d err=%v", balance, err)
	}
	t.Log("persisted 33 settled payments, reopened, drained remaining 8 without commands; idle AppHash stable")
}
