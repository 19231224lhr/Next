package member_test

import (
	"testing"
	"utxo/finality"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestOnlyOriginalDebitGetsAuthenticatedCredit(t *testing.T) {
	const chain = "credit"
	f := testkit.NewFixture(chain, "a", 1)
	tx := f.Transaction(0, 1)
	c, e := f.Certify(tx)
	if e != nil {
		t.Fatal(e)
	}
	receipt := protocol.CreditReceipt{Spend: c.QC.Fact, Resource: f.Genesis.Grants[0].Key, Original: 100, Paid: 94, Discharged: 6, Revision: 1}
	payload, _ := receipt.MarshalBinary()
	fact := protocol.FinalFact{Kind: protocol.FactCredit, Key: receipt.Key(), Revision: 1, Network: f.Org.Network, Rules: f.Schedule.IDs(), Payload: payload}
	trust, proof, e := testkit.Proof(chain, fact)
	if e != nil {
		t.Fatal(e)
	}
	for _, approved := range []bool{true, false} {
		db := store.NewMemory()
		defer db.Close()
		m, e := member.New(member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Committee: trust, Schedule: f.Schedule, Workers: 1}, db, f.Genesis)
		if e != nil {
			t.Fatal(e)
		}
		initial, _ := m.Quota(receipt.Resource, 0)
		if approved {
			if _, e = m.Approve(member.Request{Tx: tx}); e != nil {
				t.Fatal(e)
			}
		} else {
			if e = m.Install(c); e != nil {
				t.Fatal(e)
			}
		}
		if e = m.ApplyProof(proof); e != nil {
			t.Fatal(e)
		}
		if e = m.ApplyProof(proof); e != nil {
			t.Fatal(e)
		}
		quota, _ := m.Quota(receipt.Resource, 0)
		want := initial.Available
		if approved {
			want -= 94
		}
		if quota.Available != want {
			t.Fatalf("approved=%v got %+v want %d", approved, quota, want)
		}
		if !approved {
			if _, e = m.Approve(member.Request{Tx: tx}); e == nil {
				t.Fatal("first occupancy after public credit")
			}
		}
		if approved {
			receipt.Paid = 94
			receipt.Returned = 40
			receipt.Revision = 2
			payload, _ = receipt.MarshalBinary()
			fact.Payload = payload
			fact.Revision = 2
			_, newProof, _ := testkit.Proof(chain, fact)
			if e = m.ApplyProof(newProof); e != nil {
				t.Fatal(e)
			}
			if e = m.ApplyProof(proof); e != nil {
				t.Fatal(e)
			}
			quota, _ = m.Quota(receipt.Resource, 0)
			if quota.Available != want+40 || quota.Reserved != 54 {
				t.Fatalf("cumulative %+v", quota)
			}
			receipt.Returned = 0
			receipt.Revision = 1
			payload, _ = receipt.MarshalBinary()
			fact.Payload = payload
			fact.Revision = 1
		}
	}
	if _, e := finality.Verify(trust, proof); e != nil {
		t.Fatal(e)
	}
}
func TestCreditRejectsForeignCommitteeAndWrongCap(t *testing.T) {
	f := testkit.NewFixture("credit", "a", 1)
	tx := f.Transaction(0, 1)
	c, _ := f.Certify(tx)
	r := protocol.CreditReceipt{Spend: c.QC.Fact, Resource: f.Genesis.Grants[0].Key, Original: 101, Paid: 94, Discharged: 7, Revision: 1}
	raw, _ := r.MarshalBinary()
	fact := protocol.FinalFact{Kind: protocol.FactCredit, Key: r.Key(), Revision: 1, Network: f.Org.Network, Rules: f.Schedule.IDs(), Payload: raw}
	trust, p, _ := testkit.Proof("credit", fact)
	db := store.NewMemory()
	defer db.Close()
	m, e := member.New(member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Committee: trust, Schedule: rules.DefaultSchedule(), Workers: 1}, db, f.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Approve(member.Request{Tx: tx}); e != nil {
		t.Fatal(e)
	}
	before, _ := m.Quota(r.Resource, 0)
	if e = m.ApplyProof(p); e == nil {
		t.Fatal("wrong cap")
	}
	after, _ := m.Quota(r.Resource, 0)
	if before != after {
		t.Fatal("invalid receipt mutated quota")
	}
	fact.Network = protocol.Digest("NETWORK", []byte("foreign"))
	_, foreign, _ := testkit.Proof("foreign", fact)
	if e = m.ApplyProof(foreign); e == nil {
		t.Fatal("untrusted committee")
	}
	// Unsupported B release must leave the original occupancy in place.
	r.Resource = f.Genesis.Grants[2].Key
	r.Original = c.Admission[2].Cap
	r.Paid = 0
	r.Discharged = r.Original
	raw, _ = r.MarshalBinary()
	fact.Network = f.Org.Network
	fact.Kind = protocol.FactCustody
	fact.Key = r.Key()
	fact.Payload = raw
	_, p, _ = testkit.Proof("credit", fact)
	if e = m.ApplyProof(p); e == nil {
		t.Fatal("B released without local handoff")
	}
}

func TestBReleasePersistsAuthenticatedReplacementAndKeepsLocks(t *testing.T) {
	f := testkit.NewFixture("custody", "a", 1)
	tx := f.Transaction(0, 1)
	c, e := f.Certify(tx)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := c.MarshalBinary()
	receipt := protocol.CreditReceipt{Spend: c.QC.Fact, Resource: c.Admission[2].Key, Original: c.Admission[2].Cap, Discharged: c.Admission[2].Cap, Revision: 1}
	permission := protocol.CustodyReceipt{Credit: receipt, Certificate: protocol.Digest("CERTIFICATE_OBJECT", raw), Effects: c.Effects.Hash()}
	payload, _ := permission.MarshalBinary()
	fact := protocol.FinalFact{Kind: protocol.FactCustody, Key: receipt.Key(), Revision: 1, Network: f.Org.Network, Rules: f.Schedule.IDs(), Payload: payload}
	trust, proof, e := testkit.Proof("custody", fact)
	if e != nil {
		t.Fatal(e)
	}
	db := store.NewMemory()
	defer db.Close()
	m, e := member.New(member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Committee: trust, Schedule: f.Schedule, Workers: 1}, db, f.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	initial, _ := m.Quota(receipt.Resource, 0)
	if _, e = m.Approve(member.Request{Tx: tx}); e != nil {
		t.Fatal(e)
	}
	if e = m.ApplyProof(proof); e != nil {
		t.Fatal(e)
	}
	if e = m.ApplyProof(proof); e != nil {
		t.Fatal(e)
	}
	after, _ := m.Quota(receipt.Resource, 0)
	if after.Available != initial.Available || after.Reserved != 0 {
		t.Fatalf("B %+v", after)
	}
	if e = db.View(func(v state.ReadView) error {
		custody, found, e := state.Load[state.Custody](v, state.Key(state.KeyCustody, c.QC.Fact[:]))
		if !found || len(custody.Proof) == 0 {
			t.Fatal("no durable replacement")
		}
		original, found, e := state.Load[state.Approval](v, state.Key(state.KeyApproval, c.QC.Fact[:]))
		if !found || original.Fact != c.QC.Fact {
			t.Fatal("lost original signing record")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Approve(member.Request{Tx: f.Transaction(0, 2)}); e == nil {
		t.Fatal("B release cleared input lock")
	}
}
