package committee_test

import (
	"bytes"
	"context"
	abci "github.com/cometbft/cometbft/abci/types"
	"testing"
	"utxo/internal/committee"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestPublicPaymentLifecycleConservationAndReplay(t *testing.T) {
	f := testkit.NewFixture("settlement", "a", 1)
	db := store.NewMemory()
	defer db.Close()
	engine, e := committee.NewEngine(committee.EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Accounts: []committee.GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1_000_000_000}}}, db)
	if e != nil {
		t.Fatal(e)
	}
	c, e := f.Certify(f.Transaction(0, 1))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := c.MarshalBinary()
	var facts []protocol.FinalFact
	apply := func() {
		t.Helper()
		e = db.Update(func(v state.ReadView) ([]state.Change, error) {
			r, e := engine.Execute(v, raw)
			facts = r.Facts
			return r.Changes, e
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	apply()
	var payment rules.PublicPayment
	e = db.View(func(v state.ReadView) error {
		var ok bool
		var e error
		payment, ok, e = state.Load[rules.PublicPayment](v, state.Key(state.KeyPayment, c.QC.Fact[:]))
		if !ok {
			t.Fatal("missing payment")
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if !payment.Settled || !payment.Fee.Closed || payment.Fee.Rewards != 84 || payment.Fee.Burned != 10 || payment.Fee.Refunded != 6 {
		t.Fatalf("payment %+v", payment)
	}
	if len(facts) == 0 {
		t.Fatal("missing final facts")
	}
	account, e := engine.Account(f.Org.Org, protocol.AssetFUEL)
	if e != nil {
		t.Fatal(e)
	}
	if account != 1_000_000_000-94 {
		t.Fatalf("actual reserve %d", account)
	}
	for _, fact := range facts {
		if fact.Kind == protocol.FactCredit {
			r, e := protocol.DecodeCredit(fact.Payload)
			if e != nil {
				t.Fatal(e)
			}
			if r.Resource.Kind == protocol.ResourceFUEL && (r.Paid != 94 || r.Discharged != 6 || r.Remaining != 0) {
				t.Fatalf("credit %+v", r)
			}
		}
	}
	apply()
	if len(facts) != 0 {
		t.Fatal("duplicate generated new facts")
	}
	again, _ := engine.Account(f.Org.Org, protocol.AssetFUEL)
	if again != account {
		t.Fatal("duplicate charged twice")
	}
}
func TestDeferredChildRetainsRegistrationAndThenSettles(t *testing.T) {
	f := testkit.NewFixture("deferred", "a", 1)
	db := store.NewMemory()
	defer db.Close()
	engine, e := committee.NewEngine(committee.EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Accounts: []committee.GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1_000_000_000}}}, db)
	if e != nil {
		t.Fatal(e)
	}
	parent, e := f.Certify(f.Transaction(0, 1))
	if e != nil {
		t.Fatal(e)
	}
	childTx := f.Transaction(0, 2)
	childTx.Body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: parent.Effects.Outputs[0], Evidence: protocol.Hash(parent.QC.Fact)}}
	childTx = f.Sign(childTx.Body)
	child, e := f.Certify(childTx, parent)
	if e != nil {
		t.Fatal(e)
	}
	apply := func(c protocol.TXCer) {
		t.Helper()
		raw, _ := c.MarshalBinary()
		if e := db.Update(func(v state.ReadView) ([]state.Change, error) { r, e := engine.Execute(v, raw); return r.Changes, e }); e != nil {
			t.Fatal(e)
		}
	}
	apply(child)
	db.View(func(v state.ReadView) error {
		p, _, e := state.Load[rules.PublicPayment](v, state.Key(state.KeyPayment, child.QC.Fact[:]))
		if p.Settled || p.Fee.Held != 80 || p.Fee.Rewards != 20 {
			t.Fatalf("deferred %+v", p)
		}
		return e
	})
	apply(parent)
	apply(child)
	balance, _ := engine.Account(f.Org.Org, protocol.AssetFUEL)
	if balance != 1_000_000_000-188 {
		t.Fatalf("balance %d", balance)
	}
}

func TestMissingAncestorsWakeWithinBoundedBlockMaintenance(t *testing.T) {
	f := testkit.NewFixture("wake", "a", 1)
	db := store.NewMemory()
	defer db.Close()
	engine, e := committee.NewEngine(committee.EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Accounts: []committee.GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1_000_000_000}}}, db)
	if e != nil {
		t.Fatal(e)
	}
	first, e := f.Certify(f.Transaction(0, 1))
	if e != nil {
		t.Fatal(e)
	}
	chain := []protocol.TXCer{first}
	for i := 0; i < 2; i++ {
		previous := chain[len(chain)-1]
		body := f.Transaction(0, uint64(i+2)).Body
		body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: previous.Effects.Outputs[0], Evidence: protocol.Hash(previous.QC.Fact)}}
		c, e := f.Certify(f.Sign(body), previous)
		if e != nil {
			t.Fatal(e)
		}
		chain = append(chain, c)
	}
	app, e := committee.NewApp("wake", db, engine.Check, engine.Execute, engine.Drain)
	if e != nil {
		t.Fatal(e)
	}
	var commands [][]byte
	for i := len(chain) - 1; i >= 0; i-- {
		raw, _ := chain[i].MarshalBinary()
		commands = append(commands, raw)
	}
	response, e := app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: 1, Hash: bytes.Repeat([]byte{1}, 32), Txs: commands})
	if e != nil {
		t.Fatal(e)
	}
	for _, result := range response.TxResults {
		if result.Code != 0 {
			t.Fatal("command rejected")
		}
	}
	if _, e = app.Commit(context.Background(), &abci.RequestCommit{}); e != nil {
		t.Fatal(e)
	}
	for _, c := range chain {
		p, e := engine.Payment(c.QC.Fact)
		if e != nil || !p.Settled || !p.Fee.Closed {
			t.Fatalf("dependency never woken: %+v %v", p, e)
		}
	}
	balance, _ := engine.Account(f.Org.Org, protocol.AssetFUEL)
	if balance != 1_000_000_000-3*94 {
		t.Fatalf("duplicate fee: %d", balance)
	}
}
