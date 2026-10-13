package wallet_test

import (
	"testing"
	"utxo/internal/rules"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/wallet"
	"utxo/protocol"
)

func TestC3WalletRejectsUnauthenticatedPolicy(t *testing.T) {
	f := testkit.NewFixture("c3-wallet", "org", 1)
	db := store.NewMemory()
	defer db.Close()
	p := rules.DirectPolicy{C3: &rules.C3Settings{Current: protocol.Digest("accepted"), Policies: []rules.C3Policy{{ID: protocol.Digest("accepted")}}, Issuers: []rules.C3Issuer{{Config: f.Org.Hash(), Funding: protocol.AdmissionRef{Key: protocol.ResourceKey{Kind: protocol.ResourceFUEL, Account: protocol.Digest("pool"), Version: 1}, Grant: protocol.Digest("grant")}, Revenue: protocol.Digest("revenue"), Beneficiary: protocol.Digest("rewards")}}}}
	p.C3.Policies[0].Schedule = protocol.FeeSchedule{AttachReward: 1}
	w, err := wallet.NewWithGenesis(db, f.Org.Network, f.Genesis.Outputs[0].Output.Recipient.Owner, []protocol.OrgConfig{f.Org}, f.Genesis, p)
	if err != nil {
		t.Fatal(err)
	}
	i := p.C3.Issuers[0]
	out := f.Genesis.Outputs[0].Output
	c := protocol.FeeCommitment{Version: 1, Policy: p.C3.Current, Issuer: f.Org.Org, Pool: i.Funding.Key.Account, Grant: i.Funding.Grant, GrantVersion: 1, Quote: 10, Payer: out.Recipient.Owner, Revenue: i.Revenue, Beneficiary: i.Beneficiary, Resources: protocol.FeeResources{Inputs: 1, Outputs: 1, Admissions: 4, Work: protocol.WorkLimit{Execution: 1, Bytes: 1, Depth: 1, Ancestors: 1}}, Schedule: p.C3.Policies[0].Schedule, Budget: 1}
	s := protocol.OutputSummary{Network: f.Org.Network, Tx: protocol.TxID(protocol.Digest("tx")), Issuer: f.Org.Org, Config: f.Org.Hash(), Epoch: 1, Rules: f.Schedule.IDs(), C3: c, Outputs: []protocol.OutputCommitment{{Digest: protocol.OutputDigest(out), Amount: out.Amount}}}
	for _, kind := range []protocol.ResourceKind{protocol.ResourceCAL, protocol.ResourceFUEL, protocol.ResourceExecution, protocol.ResourceBytes} {
		key := protocol.ResourceKey{Kind: kind, Account: f.Org.Org, Version: 1}
		cap := uint64(1)
		grant := protocol.Digest("resource", []byte{byte(kind)})
		if kind == protocol.ResourceCAL {
			cap = 100
		}
		if kind == protocol.ResourceFUEL {
			key = i.Funding.Key
			grant = i.Funding.Grant
		}
		s.Admission = append(s.Admission, protocol.Allocation{Key: key, Cap: cap})
		s.Grants = append(s.Grants, grant)
	}
	certify := func(s protocol.OutputSummary) protocol.OutputCertificate {
		c := protocol.OutputCertificate{Summary: s}
		c.QC.Fact = s.Fact()
		for j := 0; j < 3; j++ {
			c.QC.Votes = append(c.QC.Votes, protocol.SignSpend(c.QC.Fact, uint16(j), f.Keys[j]))
		}
		return c
	}
	bad := s
	bad.C3.Policy = protocol.Digest("unknown")
	if err = w.ReceiveDirect(out, certify(bad), 0); err == nil {
		t.Fatal("unregistered fee policy accepted")
	}
	p.C3.Policies = append(p.C3.Policies, rules.C3Policy{ID: bad.C3.Policy, Schedule: c.Schedule})
	if err = w.ReceiveDirect(out, certify(bad), 0); err == nil {
		t.Fatal("caller mutated frozen wallet registry")
	}
	if err = w.ReceiveDirect(out, certify(s), 0); err != nil {
		t.Fatal("valid registry rejected", err)
	}
}
