package committee_test

import (
	"testing"
	"utxo/internal/committee"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestDirectPaymentAtomicFeeAndRoute(t *testing.T) {
	f := testkit.NewFixture("direct", "a", 1)
	retail := protocol.NewDescriptor(f.Org.Network, protocol.Route{Kind: protocol.CommitteeRoute}, f.Owner)
	f.Genesis.Outputs[0].Output.Recipient = retail
	fuel := state.OriginOutput{ID: protocol.OutputID(protocol.Digest("retail-fuel", nil)), Fact: protocol.Digest("retail-fuel-final", nil), Output: protocol.Output{Asset: protocol.AssetFUEL, Amount: 1000, Recipient: retail}}
	f.Genesis.Outputs = append(f.Genesis.Outputs, fuel)
	db := store.NewMemory()
	defer db.Close()
	engine, e := committee.NewEngine(committee.EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Accounts: []committee.GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1_000_000_000}}}, db)
	if e != nil {
		t.Fatal(e)
	}
	b := f.Transaction(0, 1).Body
	b.Kind = protocol.DirectTransfer
	b.Certifier = protocol.Hash{}
	b.Config = protocol.Hash{}
	b.Epoch = 0
	b.Admission = nil
	b.Work = protocol.WorkLimit{}
	b.Fee = protocol.FeeTerms{Source: protocol.OwnerFinalUTXO, Maximum: 100, Inputs: []protocol.Input{{Kind: protocol.FinalInput, Output: fuel.ID, Evidence: fuel.Fact}}, Refund: retail}
	tx := f.Sign(b)
	raw, _ := tx.MarshalBinary()
	var facts []protocol.FinalFact
	apply := func() error {
		return db.Update(func(v state.ReadView) ([]state.Change, error) {
			tr, e := engine.Execute(v, raw)
			facts = tr.Facts
			return tr.Changes, e
		})
	}
	if e = apply(); e != nil {
		t.Fatal(e)
	}
	if len(facts) == 0 {
		t.Fatal("no direct output proofs")
	}
	changeID := protocol.OutputIdentity(f.Org.Network, tx.Body.ID(), uint32(len(tx.Body.Outputs)))
	if e = db.View(func(v state.ReadView) error {
		change, found, e := state.Load[state.Creation](v, state.Key(state.KeyCreation, changeID[:]))
		if !found || change.Output.Asset != protocol.AssetFUEL || change.Output.Amount != 950 {
			t.Fatalf("change %+v", change)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if e = apply(); e != nil || len(facts) != 0 {
		t.Fatalf("replay facts=%d err=%v", len(facts), e)
	}
	b.Nonce[0]++
	tx = f.Sign(b)
	raw, _ = tx.MarshalBinary()
	if e = apply(); e == nil {
		t.Fatal("retail double spend")
	}
}
