package member

import (
	"context"
	"crypto/ed25519"
	"path/filepath"
	"testing"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestPoolCertificateInstallPersistsAndSupersedesMinority(t *testing.T) {
	ctx := context.Background()
	org := protocol.OrgConfig{Network: protocol.Digest("pool-archive-net"), Org: protocol.Digest("pool-archive-org"), Epoch: 1}
	var keys [4]ed25519.PrivateKey
	for i := range keys {
		seed := protocol.Digest("pool-archive-key", []byte{byte(i)})
		keys[i] = ed25519.NewKeyFromSeed(seed[:])
		copy(org.Members[i][:], keys[i].Public().(ed25519.PublicKey))
	}
	id := store.Identity{Network: org.Network.String(), Role: "member", Node: "archive-test", Schema: 1}
	path := filepath.Join(t.TempDir(), "member.db")
	db, err := store.Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	m := &Member{cfg: Config{EnableInternalPools: true, Organization: org, Key: keys[3], Index: 3}, db: db}
	desc := protocol.NewDescriptor(org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: org.Org}, keys[0])
	input := protocol.OutputID(protocol.Digest("pool-archive-input"))
	d := protocol.PoolAction{Kind: protocol.PoolDeposit, Network: org.Network, Config: org.Hash(), Input: input, Evidence: protocol.Digest("creation"), Amount: 100, To: desc}
	d.Pool = protocol.PoolIdentity(d.Network, d.Config, d.Input)
	d.Sign(keys[0])
	qc := func(a protocol.PoolAction) protocol.PoolCertificate {
		c := protocol.PoolCertificate{Action: a}
		for i := 0; i < 3; i++ {
			c.Votes = append(c.Votes, protocol.SignPool(a, uint16(i), keys[i]))
		}
		return c
	}
	dc := qc(d)
	if err = m.InstallPool(ctx, dc); err != nil {
		t.Fatal(err)
	}
	if err = db.View(func(v state.ReadView) error {
		_, found, e := state.Load[rules.PoolRecord](v, rules.PoolKey(d.Pool))
		if found {
			t.Fatal("QC installation fabricated public finality")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	a := protocol.PoolAction{Kind: protocol.PoolTransfer, Network: d.Network, Config: d.Config, Pool: d.Pool, Sequence: 1, Previous: d.ID(), Amount: 100, To: desc}
	a.Sign(keys[0])
	if err = m.InstallPool(ctx, qc(a)); err == nil {
		t.Fatal("transfer installation without public deposit")
	}
	if err = db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if e := rules.InitializeWalletBindings(o, state.Genesis{Network: org.Network, Wallets: []state.WalletBinding{{Owner: desc.Owner, Organization: org.Org}}}); e != nil {
			return nil, e
		}
		e := rules.ObservePool(o, dc, org.Network, map[protocol.Hash]protocol.OrgConfig{org.Hash(): org})
		return o.Changes(), e
	}); err != nil {
		t.Fatal(err)
	}
	minority := a
	minority.Kind = protocol.PoolExit
	minority.Sign(keys[0])
	if _, err = m.ApprovePool(ctx, protocol.PoolRequest{Action: minority, Parent: &dc}); err != nil {
		t.Fatal(err)
	}
	if err = m.InstallPool(ctx, qc(a)); err != nil {
		t.Fatal("QC must supersede minority pending", err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m = &Member{cfg: m.cfg, db: db}
	c, err := m.PoolCertificate(ctx, a.ID())
	if err != nil || c.Verify(org) != nil {
		t.Fatal("full QC unavailable after reopening", err)
	}
	checkArchive := func(parent protocol.PoolCertificate, want bool) {
		t.Helper()
		if e := db.View(func(v state.ReadView) error {
			got, e := poolParentArchived(v, parent)
			if e == nil && got != want {
				t.Fatalf("archive match = %t, want %t", got, want)
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
	}
	checkArchive(c, true)
	tamperedOwner := c
	tamperedOwner.Action.Signature[0] ^= 1 // The action ID excludes this signature.
	checkArchive(tamperedOwner, false)
	tamperedVote := c
	tamperedVote.Votes = append([]protocol.PoolVote(nil), c.Votes...)
	tamperedVote.Votes[0].Signature[0] ^= 1
	checkArchive(tamperedVote, false)
	reordered := c
	reordered.Votes = append([]protocol.PoolVote(nil), c.Votes...)
	reordered.Votes[0], reordered.Votes[1] = reordered.Votes[1], reordered.Votes[0]
	checkArchive(reordered, false)
	if _, err = m.ApprovePool(ctx, protocol.PoolRequest{Action: minority, Parent: &dc}); err != nil {
		t.Fatal("original vote archive lost", err)
	}
	next := a
	next.Kind = protocol.PoolExit
	next.Sequence = 2
	next.Previous = a.ID()
	next.Sign(keys[0])
	for _, bad := range []protocol.PoolCertificate{tamperedOwner, tamperedVote} {
		if _, err = m.ApprovePool(ctx, protocol.PoolRequest{Action: next, Parent: &bad}); err == nil {
			t.Fatal("modified same-ID parent accepted")
		}
	}
	checkArchive(qc(next), false)
	wrongOwner := next
	wrongOwner.Sign(keys[1])
	if _, err = m.ApprovePool(ctx, protocol.PoolRequest{Action: wrongOwner, Parent: &c}); err == nil {
		t.Fatal("wrong owner accepted")
	}
	wrongSequence := next
	wrongSequence.Sequence++
	wrongSequence.Sign(keys[0])
	if _, err = m.ApprovePool(ctx, protocol.PoolRequest{Action: wrongSequence, Parent: &c}); err == nil {
		t.Fatal("wrong sequence accepted")
	}
	if _, err = m.ApprovePool(ctx, protocol.PoolRequest{Action: next, Parent: &reordered}); err != nil {
		t.Fatal("differently ordered valid QC fallback failed", err)
	}
	conflict := next
	conflict.Kind = protocol.PoolTransfer
	conflict.Sign(keys[0])
	if _, err = m.ApprovePool(ctx, protocol.PoolRequest{Action: conflict, Parent: &c}); err == nil {
		t.Fatal("conflicting action accepted")
	}
	bad := qc(next)
	bad.Votes = bad.Votes[:2]
	if err = m.InstallPool(ctx, bad); err == nil {
		t.Fatal("incomplete QC accepted")
	}
}
