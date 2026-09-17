package member_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type fixture struct {
	cfg      protocol.OrgConfig
	keys     [4]ed25519.PrivateKey
	owner    ed25519.PrivateKey
	gen      state.Genesis
	schedule rules.Schedule
}

func setup(t *testing.T) fixture {
	t.Helper()
	f := fixture{schedule: rules.DefaultSchedule()}
	f.cfg.Network = protocol.Digest("NETWORK", []byte("lab"))
	f.cfg.Org = protocol.Digest("ORG", []byte("a"))
	f.cfg.Epoch = 1
	for i := range f.keys {
		p, k, _ := ed25519.GenerateKey(rand.Reader)
		f.keys[i] = k
		copy(f.cfg.Members[i][:], p)
	}
	_, f.owner, _ = ed25519.GenerateKey(rand.Reader)
	d := protocol.NewDescriptor(f.cfg.Network, protocol.Route{Kind: protocol.OrgRoute, Org: f.cfg.Org}, f.owner)
	f.gen.Network = f.cfg.Network
	f.gen.Outputs = []state.OriginOutput{{ID: protocol.OutputID(protocol.Digest("GENESIS_OUTPUT", nil)), Output: protocol.Output{Asset: protocol.AssetCAL, Amount: 100, Recipient: d}, Fact: protocol.Digest("GENESIS_FACT", nil)}}
	for _, kind := range []protocol.ResourceKind{protocol.ResourceFUEL, protocol.ResourceExecution, protocol.ResourceBytes, protocol.ResourcePolicy} {
		account := f.cfg.Org
		if kind == protocol.ResourcePolicy {
			account = protocol.Digest("POLICY", nil)
		}
		f.gen.Grants = append(f.gen.Grants, state.Grant{Organization: f.cfg.Hash(), ID: protocol.Digest("GRANT", []byte{byte(kind)}), Key: protocol.ResourceKey{Kind: kind, Account: account, Version: 1}, Amount: 10000000, Subject: d.Owner})
	}
	return f
}
func (f fixture) tx(n byte) protocol.SignedTx {
	origin := f.gen.Outputs[0]
	b := protocol.TxBody{Wire: 2, Version: 2, Network: f.cfg.Network, Kind: protocol.FastTransfer, Subject: origin.Output.Recipient.Owner, Nonce: protocol.Nonce{n}, Certifier: f.cfg.Org, Epoch: 1, Config: f.cfg.Hash(), Rules: f.schedule.IDs(), Inputs: []protocol.Input{{Kind: protocol.FinalInput, Output: origin.ID, Evidence: origin.Fact}}, Outputs: []protocol.Output{origin.Output}, Fee: protocol.FeeTerms{Source: protocol.OrgReserve, Account: f.cfg.Org, Policy: protocol.Digest("POLICY", nil), Version: 1, Maximum: 100}, Work: protocol.WorkLimit{Execution: 10000, Bytes: 1 << 20, Depth: 64, Ancestors: 256}}
	for _, g := range f.gen.Grants {
		b.Admission = append(b.Admission, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
	}
	b.Intent = b.IntentID()
	return protocol.SignedTx{Body: b, Auth: []protocol.OwnerAuth{protocol.SignOwner(b.ID(), f.owner)}}
}
func (f fixture) node(t *testing.T, i int, path string) (*member.Member, store.Store) {
	t.Helper()
	db, e := store.Open(path, store.Identity{Network: f.cfg.Network.String(), Role: "member", Node: string(rune('0' + i)), Schema: 2})
	if e != nil {
		t.Fatal(e)
	}
	m, e := member.New(member.Config{Organization: f.cfg, Index: uint16(i), Key: f.keys[i], Peers: []protocol.OrgConfig{f.cfg}, Schedule: f.schedule, Workers: 1}, db, f.gen)
	if e != nil {
		t.Fatal(e)
	}
	return m, db
}
func TestM01P02DurableApprovalAndNoDoubleSpend(t *testing.T) {
	f := setup(t)
	path := filepath.Join(t.TempDir(), "m.db")
	m, db := f.node(t, 0, path)
	tx := f.tx(1)
	first, e := m.Approve(member.Request{Tx: tx})
	if e != nil {
		t.Fatal(e)
	}
	again, e := m.Approve(member.Request{Tx: tx})
	if e != nil || again.Vote != first.Vote || again.Fact != first.Fact {
		t.Fatal("retry changed vote")
	}
	if _, e = m.Approve(member.Request{Tx: f.tx(2)}); e == nil {
		t.Fatal("conflicting vote")
	}
	db.Close()
	m, db = f.node(t, 0, path)
	defer db.Close()
	again, e = m.Approve(member.Request{Tx: tx})
	if e != nil || again.Vote != first.Vote {
		t.Fatal("restart lost signed intent")
	}
	if _, e = m.Approve(member.Request{Tx: f.tx(2)}); e == nil {
		t.Fatal("restart forgot lock")
	}
}
func TestQ01CertificateCanBeSpentBeforeInstall(t *testing.T) {
	f := setup(t)
	members := make([]*member.Member, 4)
	for i := range members {
		m, db := f.node(t, i, filepath.Join(t.TempDir(), "m.db"))
		defer db.Close()
		members[i] = m
	}
	tx := f.tx(1)
	var c protocol.TXCer
	for i := 0; i < 3; i++ {
		r, e := members[i].Approve(member.Request{Tx: tx})
		if e != nil {
			t.Fatal(e)
		}
		c.Tx = tx
		c.Admission = r.Admission
		c.Effects = r.Effects
		c.QC.Fact = r.Fact
		c.QC.Votes = append(c.QC.Votes, r.Vote)
	}
	if e := c.Verify(f.cfg); e != nil {
		t.Fatal(e)
	}
	child := f.tx(2)
	child.Body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: c.Effects.Outputs[0], Evidence: protocol.Hash(c.QC.Fact)}}
	child.Body.Intent = child.Body.IntentID()
	child.Auth = []protocol.OwnerAuth{protocol.SignOwner(child.Body.ID(), f.owner)}
	for i := 0; i < 3; i++ {
		if _, e := members[i].Approve(member.Request{Tx: child, Parents: []protocol.TXCer{c}}); e != nil {
			t.Fatal(e)
		}
	}
	for _, m := range members {
		if e := m.Install(c); e != nil {
			t.Fatal(e)
		}
	}
	conflict := child
	conflict.Body.Nonce[0] = 3
	conflict.Body.Intent = conflict.Body.IntentID()
	conflict.Auth = []protocol.OwnerAuth{protocol.SignOwner(conflict.Body.ID(), f.owner)}
	for i := 0; i < 3; i++ {
		if _, e := members[i].Approve(member.Request{Tx: conflict, Parents: []protocol.TXCer{c}}); e == nil {
			t.Fatal("late install revived consumed child input")
		}
	}
}

func TestRepeatedInstallDoesNotReopenCompletedOutbox(t *testing.T) {
	f := setup(t)
	m, db := f.node(t, 0, filepath.Join(t.TempDir(), "member.db"))
	defer db.Close()
	tx := f.tx(1)
	var c protocol.TXCer
	for i := 0; i < 3; i++ {
		other, otherDB := f.node(t, i, filepath.Join(t.TempDir(), "vote.db"))
		a, e := other.Approve(member.Request{Tx: tx})
		otherDB.Close()
		if e != nil {
			t.Fatal(e)
		}
		c.Tx = tx
		c.Admission = a.Admission
		c.Effects = a.Effects
		c.QC.Fact = a.Fact
		c.QC.Votes = append(c.QC.Votes, a.Vote)
	}
	if e := m.Install(c); e != nil {
		t.Fatal(e)
	}
	if e := db.Update(func(state.ReadView) ([]state.Change, error) {
		return []state.Change{{Key: state.Key(state.KeyOutbox, c.QC.Fact[:]), Delete: true}}, nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := m.Install(c); e != nil {
		t.Fatal(e)
	}
	if e := db.View(func(v state.ReadView) error { _, e := v.Get(state.Key(state.KeyOutbox, c.QC.Fact[:])); return e }); !errors.Is(e, state.ErrNotFound) {
		t.Fatalf("completed outbox reopened: %v", e)
	}
}
