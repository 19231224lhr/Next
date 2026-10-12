package wallet

import (
	"crypto/ed25519"
	"testing"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestPoolRejectsInvalidLocalProposalBeforeLock(t *testing.T) {
	f := testkit.NewFixture("pool-wallet", "org", 1)
	db := store.NewMemory()
	defer db.Close()
	origin := f.Genesis.Outputs[0]
	w, e := NewWithGenesis(db, f.Org.Network, origin.Output.Recipient.Owner, []protocol.OrgConfig{f.Org}, f.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	d := protocol.PoolAction{Kind: protocol.PoolDeposit, Network: f.Org.Network, Config: f.Org.Hash(), Input: origin.ID, Evidence: origin.Fact, Amount: 100, To: origin.Output.Recipient}
	d.Pool = protocol.PoolIdentity(d.Network, d.Config, d.Input)
	d.Sign(f.Owner)
	bad := d
	bad.Amount = 200
	bad.Sign(f.Owner)
	if e = w.SavePoolRequest(protocol.PoolRequest{Action: bad}); e == nil {
		t.Fatal("persisted inflated deposit")
	}
	if e = w.SavePoolRequest(protocol.PoolRequest{Action: d}); e != nil {
		t.Fatal("bad deposit poisoned corrected proposal", e)
	}
	parent := protocol.PoolCertificate{Action: d}
	for i := 0; i < 3; i++ {
		parent.Votes = append(parent.Votes, protocol.SignPool(d, uint16(i), f.Keys[i]))
	}
	a := protocol.PoolAction{Kind: protocol.PoolTransfer, Network: d.Network, Config: d.Config, Pool: d.Pool, Amount: 100, Sequence: 1, Previous: d.ID(), To: d.To}
	a.Sign(f.Owner)
	unknown := protocol.Digest("unknown owner")
	k := ed25519.NewKeyFromSeed(unknown[:])
	bad = a
	bad.To = protocol.NewDescriptor(d.Network, d.To.Route, k)
	bad.Sign(f.Owner)
	if e = w.SavePoolRequest(protocol.PoolRequest{Action: bad, Parent: &parent}); e == nil {
		t.Fatal("persisted unregistered recipient")
	}
	if e = w.SavePoolRequest(protocol.PoolRequest{Action: a, Parent: &parent}); e != nil {
		t.Fatal("bad recipient poisoned corrected proposal", e)
	}
}
