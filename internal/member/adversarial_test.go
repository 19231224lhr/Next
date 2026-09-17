package member_test

import (
	"path/filepath"
	"testing"
	"utxo/internal/member"
	"utxo/protocol"
)

func TestM03FailedBudgetDoesNotLockInput(t *testing.T) {
	f := setup(t)
	for i := range f.gen.Grants {
		if f.gen.Grants[i].Key.Kind == protocol.ResourceFUEL {
			f.gen.Grants[i].Amount = 150
		}
	}
	m, db := f.node(t, 0, filepath.Join(t.TempDir(), "m.db"))
	defer db.Close()
	bad := f.tx(1)
	bad.Body.Fee.Maximum = 101
	bad.Auth = []protocol.OwnerAuth{protocol.SignOwner(bad.Body.ID(), f.owner)}
	if _, e := m.Approve(member.Request{Tx: bad}); e == nil {
		t.Fatal("over budget accepted")
	}
	if _, e := m.Approve(member.Request{Tx: f.tx(2)}); e != nil {
		t.Fatalf("rejection left input locked: %v", e)
	}
}
func TestM04M05ConflictingLocalVoteCanInstallQuorumWithoutCredit(t *testing.T) {
	f := setup(t)
	nodes := make([]*member.Member, 4)
	for i := range nodes {
		m, db := f.node(t, i, filepath.Join(t.TempDir(), "m.db"))
		defer db.Close()
		nodes[i] = m
	}
	u, e := nodes[3].Approve(member.Request{Tx: f.tx(2)})
	if e != nil {
		t.Fatal(e)
	}
	tx := f.tx(1)
	var c protocol.TXCer
	for i := 0; i < 3; i++ {
		r, e := nodes[i].Approve(member.Request{Tx: tx})
		if e != nil {
			t.Fatal(e)
		}
		c.Tx = tx
		c.Admission = r.Admission
		c.Effects = r.Effects
		c.QC.Fact = r.Fact
		c.QC.Votes = append(c.QC.Votes, r.Vote)
	}
	key := u.Admission[0].Key
	before, _ := nodes[3].Quota(key, 0)
	if e = nodes[3].Install(c); e != nil {
		t.Fatal(e)
	}
	after, _ := nodes[3].Quota(key, 0)
	if before != after {
		t.Fatal("installation released or charged budget")
	}
	if _, e = nodes[3].Approve(member.Request{Tx: f.tx(3)}); e == nil {
		t.Fatal("installed conflict signed")
	}
}
func TestX01CrossOrganizationWithoutParentInstall(t *testing.T) {
	a := setup(t)
	b := setup(t)
	b.cfg.Network = a.cfg.Network
	b.cfg.Org = protocol.Digest("ORG", []byte("b"))
	b.gen.Network = a.cfg.Network
	desc := protocol.NewDescriptor(a.cfg.Network, protocol.Route{Kind: protocol.OrgRoute, Org: b.cfg.Org}, b.owner)
	for i := range b.gen.Grants {
		b.gen.Grants[i].Organization = b.cfg.Hash()
		b.gen.Grants[i].Subject = desc.Owner
		if b.gen.Grants[i].Key.Kind != protocol.ResourcePolicy {
			b.gen.Grants[i].Key.Account = b.cfg.Org
		}
	}
	b.gen.Outputs = nil
	tx := a.tx(1)
	tx.Body.Outputs[0].Recipient = desc
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.Body.ID(), a.owner)}
	var cert protocol.TXCer
	for i := 0; i < 3; i++ {
		m, db := a.node(t, i, filepath.Join(t.TempDir(), "a.db"))
		defer db.Close()
		r, e := m.Approve(member.Request{Tx: tx})
		if e != nil {
			t.Fatal(e)
		}
		cert.Tx = tx
		cert.Admission = r.Admission
		cert.Effects = r.Effects
		cert.QC.Fact = r.Fact
		cert.QC.Votes = append(cert.QC.Votes, r.Vote)
	}
	child := a.tx(2)
	child.Body.Subject = desc.Owner
	child.Body.Certifier = b.cfg.Org
	child.Body.Config = b.cfg.Hash()
	child.Body.Fee.Account = b.cfg.Org
	child.Body.Admission = nil
	for _, g := range b.gen.Grants {
		child.Body.Admission = append(child.Body.Admission, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
	}
	child.Body.Intent = child.Body.IntentID()
	child.Body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: cert.Effects.Outputs[0], Evidence: protocol.Hash(cert.QC.Fact)}}
	child.Body.Outputs[0].Amount = 100
	child.Body.Outputs[0].Recipient = a.gen.Outputs[0].Output.Recipient
	child.Auth = []protocol.OwnerAuth{protocol.SignOwner(child.Body.ID(), b.owner)}
	var childCert protocol.TXCer
	for i := 0; i < 3; i++ {
		m, db := b.node(t, i, filepath.Join(t.TempDir(), "b.db"))
		defer db.Close()
		// Reopen with the same durable identity and a trusted peer configuration.
		m, e := member.New(member.Config{Organization: b.cfg, Index: uint16(i), Key: b.keys[i], Peers: []protocol.OrgConfig{a.cfg, b.cfg}, Schedule: b.schedule, Workers: 1}, db, b.gen)
		if e != nil {
			t.Fatal(e)
		}
		r, e := m.Approve(member.Request{Tx: child, Parents: []protocol.TXCer{cert}})
		if e != nil {
			t.Fatal(e)
		}
		childCert.Tx = child
		childCert.Admission = r.Admission
		childCert.Effects = r.Effects
		childCert.QC.Fact = r.Fact
		childCert.QC.Votes = append(childCert.QC.Votes, r.Vote)
	}
	if e := childCert.Verify(b.cfg); e != nil {
		t.Fatal(e)
	}
	for _, r := range childCert.Admission {
		if r.Key.Kind == protocol.ResourceCAL {
			t.Fatal("cross-org transfer added principal coverage")
		}
	}
}
