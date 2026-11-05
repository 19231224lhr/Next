package member_test

import (
	"bytes"
	"testing"
	"utxo/finality"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

type countedStore struct {
	store.Store
	updates int
	before  func()
}

func (s *countedStore) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	s.updates++
	if s.before != nil {
		f := s.before
		s.before = nil
		f()
	}
	return s.Store.Update(fn)
}

func batchFixture(t *testing.T) (*member.Member, *countedStore, protocol.TXCer, []finality.FactProof) {
	t.Helper()
	f := testkit.NewFixture("batch-credit", "a", 1)
	tx := f.Transaction(0, 1)
	c, err := f.Certify(tx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := c.MarshalBinary()
	var trust finality.Trust
	var proofs []finality.FactProof
	for _, a := range c.Admission {
		r := protocol.CreditReceipt{Spend: c.QC.Fact, Resource: a.Key, Original: a.Cap, Revision: 1, Discharged: a.Cap}
		kind := protocol.FactCredit
		switch a.Key.Kind {
		case protocol.ResourceFUEL, protocol.ResourcePolicy:
			r.Paid = 94
			r.Discharged -= 94
		case protocol.ResourceExecution:
			kind = protocol.FactWork
		case protocol.ResourceBytes:
			kind = protocol.FactCustody
		}
		payload, _ := r.MarshalBinary()
		if kind == protocol.FactCustody {
			payload, _ = (protocol.CustodyReceipt{Credit: r, Certificate: protocol.Digest("CERTIFICATE_OBJECT", raw), Effects: c.Effects.Hash()}).MarshalBinary()
		}
		fact := protocol.FinalFact{Kind: kind, Key: r.Key(), Revision: 1, Network: f.Org.Network, Rules: c.Tx.Body.Rules, Payload: payload}
		var p finality.FactProof
		trust, p, err = testkit.Proof("batch-credit", fact)
		if err != nil {
			t.Fatal(err)
		}
		proofs = append(proofs, p)
	}
	db := &countedStore{Store: store.NewMemory()}
	t.Cleanup(func() { db.Close() })
	m, err := member.New(member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Peers: []protocol.OrgConfig{f.Org}, Committee: trust, Schedule: f.Schedule, Workers: 1}, db, f.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Approve(member.Request{Tx: tx}); err != nil {
		t.Fatal(err)
	}
	if err = m.Install(c); err != nil {
		t.Fatal(err)
	}
	db.updates = 0
	return m, db, c, proofs
}

func TestBatchCreditIsAtomicAndRetryDoesNotRefundTwice(t *testing.T) {
	m, db, c, proofs := batchFixture(t)
	progress, err := m.ApplyReceipts(c, proofs)
	if err != nil {
		t.Fatal(err)
	}
	if !progress.Complete || db.updates != 1 {
		t.Fatalf("progress=%+v updates=%d", progress, db.updates)
	}
	out, err := m.Outcome(c.QC.Fact)
	if err != nil {
		t.Fatal(err)
	}
	if out.FuelResidual != 94 || out.PolicyResidual != 94 || out.ExecutionResidual != 0 || out.BytesResidual != 0 || !out.Custody {
		t.Fatalf("lost a resource update: %+v", out)
	}
	if err = db.View(func(v state.ReadView) error { _, e := v.Get(state.Key(state.KeyOutbox, c.QC.Fact[:])); return e }); err != state.ErrNotFound {
		t.Fatal("outbox retained", err)
	}
	if _, err = m.ApplyReceipts(c, proofs); err != nil {
		t.Fatal(err)
	}
	after, _ := m.Outcome(c.QC.Fact)
	if after != out {
		t.Fatal("retry changed residuals")
	}
	for _, p := range proofs {
		if p.Fact.Kind == protocol.FactCustody {
			db.View(func(v state.ReadView) error {
				saved, _, e := state.Load[state.Custody](v, state.Key(state.KeyCustody, c.QC.Fact[:]))
				raw, _ := p.MarshalBinary()
				if !bytes.Equal(raw, saved.Proof) {
					t.Fatal("saved different proof")
				}
				return e
			})
		}
	}
}

func TestBatchCreditMissingAndInvalidProofs(t *testing.T) {
	m, db, c, proofs := batchFixture(t)
	bad := append([]finality.FactProof(nil), proofs...)
	bad[len(bad)-1].Fact.Payload = bytes.Clone(bad[len(bad)-1].Fact.Payload)
	bad[len(bad)-1].Fact.Payload[0] ^= 1
	before, _ := m.Outcome(c.QC.Fact)
	if _, e := m.ApplyReceipts(c, bad); e == nil {
		t.Fatal("accepted invalid proof")
	}
	after, _ := m.Outcome(c.QC.Fact)
	if after != before || db.updates != 0 {
		t.Fatal("invalid batch partly committed")
	}
	progress, e := m.ApplyReceipts(c, proofs[:1])
	if e != nil || progress.Complete {
		t.Fatal(progress, e)
	}
	if e = db.View(func(v state.ReadView) error { _, e := v.Get(state.Key(state.KeyOutbox, c.QC.Fact[:])); return e }); e != nil {
		t.Fatal("partial batch deleted outbox")
	}
	if _, e = m.ApplyReceipts(c, proofs); e != nil {
		t.Fatal(e)
	}
}

func TestInstallRechecksInsideTransaction(t *testing.T) {
	f := testkit.NewFixture("install-race", "a", 1)
	c, e := f.Certify(f.Transaction(0, 1))
	if e != nil {
		t.Fatal(e)
	}
	db := &countedStore{Store: store.NewMemory()}
	defer db.Close()
	m, e := member.New(member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Peers: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Workers: 1}, db, f.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := c.MarshalBinary()
	pending := state.Outbox{Fact: c.QC.Fact, Certificate: raw, Attempt: []byte("already submitted"), NextSubmitUnixNS: 123, PublicComplete: true}
	// Another INSTALL and relay update commit after the optimistic read.
	db.before = func() {
		e := db.Store.Update(func(v state.ReadView) ([]state.Change, error) {
			o := state.NewOverlay(v)
			o.Set(state.Key(state.KeyInstall, c.QC.Fact[:]), raw)
			e := state.Put(o, state.Key(state.KeyOutbox, c.QC.Fact[:]), pending)
			return o.Changes(), e
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = m.Install(c); e != nil {
		t.Fatal(e)
	}
	db.View(func(v state.ReadView) error {
		p, _, e := state.Load[state.Outbox](v, state.Key(state.KeyOutbox, c.QC.Fact[:]))
		if !bytes.Equal(p.Attempt, pending.Attempt) || p.NextSubmitUnixNS != 123 || !p.PublicComplete {
			t.Fatal("INSTALL reset relay progress")
		}
		return e
	})
}

func TestParentAdmissionPreservesDeliveryProgress(t *testing.T) {
	f := testkit.NewFixture("parent-progress", "a", 1)
	tx := f.Transaction(0, 1)
	c, e := f.Certify(tx)
	if e != nil {
		t.Fatal(e)
	}
	db := store.NewMemory()
	defer db.Close()
	if _, e = member.New(member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Schedule: f.Schedule, Workers: 1}, db, f.Genesis); e != nil {
		t.Fatal(e)
	}
	parent := state.Outbox{Fact: protocol.SpendFactID(protocol.Digest("parent")), Certificate: []byte("retained"), Attempt: []byte("submitted"), NextSubmitUnixNS: 42}
	db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		e := state.Put(o, state.Key(state.KeyOutbox, parent.Fact[:]), parent)
		return o.Changes(), e
	})
	material := parent
	material.Attempt = nil
	material.NextSubmitUnixNS = 0
	origin := f.Genesis.Outputs[0]
	if e = db.Update(func(v state.ReadView) ([]state.Change, error) {
		cs, _, e := rules.EvaluatePrepare(v, tx, []state.Creation{{Output: origin.Output, Fact: origin.Fact, Final: true}}, c.Admission, c.Effects, 0, []state.Outbox{material})
		return cs, e
	}); e != nil {
		t.Fatal(e)
	}
	db.View(func(v state.ReadView) error {
		p, _, e := state.Load[state.Outbox](v, state.Key(state.KeyOutbox, parent.Fact[:]))
		if p.NextSubmitUnixNS != 42 || !bytes.Equal(p.Attempt, parent.Attempt) {
			t.Fatal("parent reset delivery progress")
		}
		return e
	})
}

func TestFirstInstallPreservesPendingSubmission(t *testing.T) {
	f := testkit.NewFixture("first-install", "a", 1)
	c, e := f.Certify(f.Transaction(0, 1))
	if e != nil {
		t.Fatal(e)
	}
	db := store.NewMemory()
	defer db.Close()
	m, e := member.New(member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Peers: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Workers: 1}, db, f.Genesis)
	if e != nil {
		t.Fatal(e)
	}
	key := state.Key(state.KeyOutbox, c.QC.Fact[:])
	pending := state.Outbox{Fact: c.QC.Fact, Origin: f.Org.Org, Attempt: []byte("submitted"), NextSubmitUnixNS: 42, PublicComplete: true}
	if e = db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		e := state.Put(o, key, pending)
		return o.Changes(), e
	}); e != nil {
		t.Fatal(e)
	}
	if e = m.Install(c); e != nil {
		t.Fatal(e)
	}
	if e = db.View(func(v state.ReadView) error {
		p, _, e := state.Load[state.Outbox](v, key)
		if p.NextSubmitUnixNS != 42 || !p.PublicComplete || !bytes.Equal(p.Attempt, pending.Attempt) || len(p.Certificate) == 0 {
			t.Fatal("first INSTALL reset pending submission")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
