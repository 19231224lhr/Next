package gateway

import (
	"context"
	"path/filepath"
	"testing"
	"time"
	"utxo/finality"
	"utxo/internal/committee"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type blockedInstaller struct{}

func (blockedInstaller) Approve(context.Context, protocol.PaymentRequest) (protocol.Approval, error) {
	return protocol.Approval{}, protocol.ErrUnsupported
}
func (blockedInstaller) Install(ctx context.Context, c protocol.TXCer) error {
	<-ctx.Done()
	return ctx.Err()
}

type provenPublic struct {
	proofs      map[protocol.Hash]finality.FactProof
	certificate protocol.TXCer
}

func (p provenPublic) Submit(context.Context, []byte) error { return nil }
func (p provenPublic) Certificate(context.Context, protocol.SpendFactID) (protocol.TXCer, error) {
	return p.certificate, nil
}
func (p provenPublic) Receipt(_ context.Context, kind protocol.FactKind, key protocol.Hash) (finality.FactProof, error) {
	proof, ok := p.proofs[key]
	if !ok || proof.Fact.Kind != kind {
		return finality.FactProof{}, state.ErrNotFound
	}
	return proof, nil
}
func TestPublicCustodyCompletesRelayWithoutInstallAck(t *testing.T) {
	f := testkit.NewFixture("relay", "a", 1)
	c, e := f.Certify(f.Transaction(0, 1))
	if e != nil {
		t.Fatal(e)
	}
	db := store.NewMemory()
	defer db.Close()
	engine, e := committee.NewEngine(committee.EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Accounts: []committee.GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1_000_000_000}}}, db)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := c.MarshalBinary()
	var transition state.Transition
	e = db.View(func(v state.ReadView) error { var e error; transition, e = engine.Execute(v, raw); return e })
	if e != nil {
		t.Fatal(e)
	}
	public := provenPublic{proofs: make(map[protocol.Hash]finality.FactProof), certificate: c}
	var trust finality.Trust
	for _, fact := range transition.Facts {
		var proof finality.FactProof
		trust, proof, e = testkit.Proof("relay", fact)
		if e != nil {
			t.Fatal(e)
		}
		public.proofs[fact.Key] = proof
	}
	key := state.Key(state.KeyOutbox, c.QC.Fact[:])
	pending := state.Outbox{Fact: c.QC.Fact, Certificate: raw, Origin: f.Org.Org}

	// No collector copy exists. Receive persists the only complete certificate,
	// then a fresh wallet store is opened as after a wallet-process restart.
	path := filepath.Join(t.TempDir(), "wallet.db")
	identity := store.Identity{Network: f.Org.Network.String(), Role: "wallet", Node: "recipient", Schema: 2}
	walletDB, e := store.Open(path, identity)
	if e != nil {
		t.Fatal(e)
	}
	w, e := wallet.New(walletDB, f.Org.Network, c.Tx.Body.Outputs[0].Recipient.Owner, []protocol.OrgConfig{f.Org})
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Receive(c, 0); e != nil {
		t.Fatal(e)
	}
	if e = walletDB.Close(); e != nil {
		t.Fatal(e)
	}
	walletDB, e = store.Open(path, identity)
	if e != nil {
		t.Fatal(e)
	}
	defer walletDB.Close()
	relay := Relay{DB: walletDB, Public: public, Trust: trust, Organizations: map[protocol.Hash]protocol.OrgConfig{f.Org.Hash(): f.Org}, Members: map[protocol.Hash][4]MemberClient{f.Org.Org: {blockedInstaller{}, blockedInstaller{}, blockedInstaller{}, blockedInstaller{}}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = relay.deliver(ctx, key, pending); e != nil {
		t.Fatal(e)
	}
	if e = walletDB.View(func(v state.ReadView) error { _, e := v.Get(key); return e }); e != state.ErrNotFound {
		t.Fatal("completed outbox retained")
	}
}
