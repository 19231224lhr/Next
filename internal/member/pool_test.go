package member

import (
	"context"
	"crypto/ed25519"
	"testing"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestPoolVoteLockSurvivesRestartAndExitCompetes(t *testing.T) {
	net := protocol.Digest("pool-member-test")
	org := protocol.OrgConfig{Network: net, Org: protocol.Digest("pool-org"), Epoch: 1}
	var keys [4]ed25519.PrivateKey
	for i := range keys {
		s := protocol.Digest("member", []byte{byte(i)})
		keys[i] = ed25519.NewKeyFromSeed(s[:])
		copy(org.Members[i][:], keys[i].Public().(ed25519.PublicKey))
	}
	seed := protocol.Digest("owner")
	owner := ed25519.NewKeyFromSeed(seed[:])
	desc := protocol.NewDescriptor(net, protocol.Route{Kind: protocol.OrgRoute, Org: org.Org}, owner)
	origin := state.OriginOutput{ID: protocol.OutputID(protocol.Digest("input")), Fact: protocol.Digest("creation"), Output: protocol.Output{Asset: protocol.AssetCAL, Amount: 100, Recipient: desc}}
	db := store.NewMemory()
	defer db.Close()
	// Exercise the real member method against persisted state; re-create the
	// process object against the same store without discarding pending votes.
	m := &Member{cfg: Config{Organization: org, Index: 0, Key: keys[0], EnableInternalPools: true}, db: db}
	if err := db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if e := rules.InitializeWalletBindings(o, state.Genesis{Network: net, Outputs: []state.OriginOutput{origin}}); e != nil {
			return nil, e
		}
		if e := state.Put(o, rules.DirectCreationKey(origin.ID, 0), state.Creation{Output: origin.Output, Fact: origin.Fact, Final: true}); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	}); err != nil {
		t.Fatal(err)
	}
	deposit := protocol.PoolAction{Kind: protocol.PoolDeposit, Network: net, Config: org.Hash(), Pool: protocol.PoolIdentity(net, org.Hash(), origin.ID), Input: origin.ID, Evidence: origin.Fact, Amount: 100, To: desc}
	deposit.Sign(owner)
	req := protocol.PoolRequest{Action: deposit}
	if _, err := m.ApprovePool(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	cert := func(a protocol.PoolAction) protocol.PoolCertificate {
		c := protocol.PoolCertificate{Action: a}
		for i := 0; i < 3; i++ {
			c.Votes = append(c.Votes, protocol.SignPool(a, uint16(i), keys[i]))
		}
		return c
	}
	dc := cert(deposit)
	if err := db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		e := rules.ObservePool(o, dc, net, map[protocol.Hash]protocol.OrgConfig{org.Hash(): org})
		return o.Changes(), e
	}); err != nil {
		t.Fatal(err)
	}
	next := protocol.PoolAction{Kind: protocol.PoolTransfer, Network: net, Config: org.Hash(), Pool: deposit.Pool, Sequence: 1, Previous: deposit.ID(), Amount: 100, To: desc}
	next.Sign(owner)
	if _, err := m.ApprovePool(context.Background(), protocol.PoolRequest{Action: next, Parent: &dc}); err != nil {
		t.Fatal(err)
	}
	restarted := &Member{cfg: m.cfg, db: db}
	exit := next
	exit.Kind = protocol.PoolExit
	exit.Sign(owner)
	if _, err := restarted.ApprovePool(context.Background(), protocol.PoolRequest{Action: exit, Parent: &dc}); err == nil {
		t.Fatal("conflicting exit after restart")
	}
	// Next request carries the QC that this member never received separately.
	nc := cert(next)
	exit.Sequence = 2
	exit.Previous = next.ID()
	exit.Sign(owner)
	if _, err := restarted.ApprovePool(context.Background(), protocol.PoolRequest{Action: exit, Parent: &nc}); err != nil {
		t.Fatal("QC catchup", err)
	}
	move := exit
	move.Kind = protocol.PoolTransfer
	move.Sign(owner)
	if _, err := restarted.ApprovePool(context.Background(), protocol.PoolRequest{Action: move, Parent: &nc}); err == nil {
		t.Fatal("transfer after exit vote")
	}
}
