package rules

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"

	"utxo/crypto/chameleon"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type directFixture struct {
	db      store.Store
	policy  DirectPolicy
	org     protocol.OrgConfig
	members [4]ed25519.PrivateKey
	owner   ed25519.PrivateKey
	genesis protocol.OutputID
	output  protocol.Output
	grants  []protocol.AdmissionRef
}

func newDirectFixture(t *testing.T) directFixture {
	t.Helper()
	f := directFixture{db: store.NewMemory()}
	t.Cleanup(func() { f.db.Close() })
	net := protocol.Digest("direct-test")
	f.org.Network = net
	f.org.Org = protocol.Digest("org")
	f.org.Epoch = 1
	for i := range f.members {
		pub, key, _ := ed25519.GenerateKey(rand.Reader)
		f.members[i] = key
		copy(f.org.Members[i][:], pub)
	}
	_, f.owner, _ = ed25519.GenerateKey(rand.Reader)
	f.output = protocol.Output{Asset: protocol.AssetCAL, Amount: 100, Recipient: protocol.NewDescriptor(net, protocol.Route{Kind: protocol.OrgRoute, Org: f.org.Org}, f.owner)}
	f.genesis = protocol.OutputID(protocol.Digest("genesis-output"))
	b, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(b)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := chameleon.NewPublic(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	f.policy = DirectPolicy{Base: DefaultSchedule(), TimeoutSeconds: 30, RepairCost: 5, Key: pub, Organizations: map[protocol.Hash]protocol.OrgConfig{f.org.Hash(): f.org}}
	for _, kind := range []protocol.ResourceKind{protocol.ResourceCAL, protocol.ResourceFUEL, protocol.ResourceExecution, protocol.ResourceBytes, protocol.ResourcePolicy} {
		account := f.org.Org
		if kind == protocol.ResourcePolicy {
			account = protocol.Digest("policy")
		}
		f.grants = append(f.grants, protocol.AdmissionRef{Key: protocol.ResourceKey{Kind: kind, Account: account, Version: 1}, Grant: protocol.Digest("grant", []byte{byte(kind)})})
	}
	err = f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if err := state.Put(o, DirectCreationKey(f.genesis, 0), state.Creation{Output: f.output, Fact: protocol.Digest("genesis-fact"), Final: true}); err != nil {
			return nil, err
		}
		for _, g := range f.grants {
			if err := state.Put(o, state.Key(state.KeyGrant, g.Key.Encode()), state.Grant{ID: g.Grant, Organization: f.org.Hash(), Key: g.Key, Amount: 10000000, Subject: f.output.Recipient.Owner}); err != nil {
				return nil, err
			}
		}
		for _, asset := range []protocol.Asset{protocol.AssetCAL, protocol.AssetFUEL} {
			if err := state.Put(o, AccountKey(f.org.Org, asset), uint64(10000000)); err != nil {
				return nil, err
			}
		}
		return o.Changes(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f directFixture) payment(t *testing.T, input protocol.OutputID, parent *protocol.OutputCertificate, nonce byte) DirectPayment {
	t.Helper()
	in := protocol.Input{Kind: protocol.FinalInput, Output: input, Evidence: protocol.Digest("genesis-fact")}
	var parents []DirectParent
	if parent != nil {
		in.Kind = protocol.CertificateInput
		in.Evidence = protocol.Hash(parent.QC.Fact)
		parents = []DirectParent{{Certificate: *parent, Index: 0}}
	}
	body := protocol.TxBody{Wire: 3, Version: 3, Network: f.org.Network, Kind: protocol.FastTransfer, Subject: f.output.Recipient.Owner, Certifier: f.org.Org, Config: f.org.Hash(), Epoch: 1, Rules: f.policy.Rules(), Inputs: []protocol.Input{in}, Outputs: []protocol.Output{f.output}, Admission: f.grants,
		Fee: protocol.FeeTerms{Source: protocol.OrgReserve, Account: f.org.Org, Policy: protocol.Digest("policy"), Version: 1, Maximum: 1000}, Work: protocol.WorkLimit{Execution: 100000, Bytes: 10000000, Depth: 1, Ancestors: 1}}
	body.Nonce[0] = nonce
	body.Intent = body.IntentID()
	tx, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: f.output}}, f.policy.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
	vector, err := PrepareDirectVector(tx, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	c := protocol.OutputCertificate{Summary: protocol.SummaryFor(tx, vector)}
	c.QC.Fact = c.Summary.Fact()
	for i := 0; i < 3; i++ {
		c.QC.Votes = append(c.QC.Votes, protocol.SignSpend(c.QC.Fact, uint16(i), f.members[i]))
	}
	return DirectPayment{Tx: tx, Certificate: c, Parents: parents}
}

func (f directFixture) settle(t *testing.T, p DirectPayment, now int64) state.Transition {
	t.Helper()
	verified, err := VerifyDirectPayment(p, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	var tr state.Transition
	err = f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		var err error
		tr, err = EvaluateDirectPayment(v, verified, f.policy, now)
		return tr.Changes, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func loadDirect[T any](t *testing.T, db store.Store, key []byte) T {
	t.Helper()
	var result T
	err := db.View(func(v state.ReadView) error {
		var found bool
		var err error
		result, found, err = state.Load[T](v, key)
		if err == nil && !found {
			t.Fatal("missing state")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDirectChildBeforeParentAndImmediateSuccessor(t *testing.T) {
	f := newDirectFixture(t)
	parent := f.payment(t, f.genesis, nil, 1)
	pid := parent.Certificate.Summary.OutputID(0)
	child := f.payment(t, pid, &parent.Certificate, 2)
	f.settle(t, child, 100)
	id := child.Certificate.Summary.OutputID(0)
	if got := loadDirect[state.Creation](t, f.db, DirectCreationKey(id, 0)); !got.Final {
		t.Fatal("child still waiting for parent")
	}
	grand := f.payment(t, id, &child.Certificate, 3)
	f.settle(t, grand, 101)
	ob := loadDirect[DirectObligation](t, f.db, DirectObligationKey(pid))
	if ob.Status != DirectOpen {
		t.Fatal("missing direct responsibility")
	}
	f.settle(t, parent, 102)
	ob = loadDirect[DirectObligation](t, f.db, DirectObligationKey(pid))
	if ob.Status != DirectFulfilled {
		t.Fatal("parent did not discharge direct responsibility")
	}
	if got := loadDirect[protocol.CreditReceipt](t, f.db, DirectCALCreditKey(parent.Certificate.QC.Fact)); got.Discharged != 100 || got.Paid != 0 {
		t.Fatalf("bad credit: %+v", got)
	}
	if len(f.settle(t, child, 103).Changes) != 0 {
		t.Fatal("duplicate payment not idempotent")
	}
}

func TestDirectRepairThenLateParent(t *testing.T) {
	f := newDirectFixture(t)
	parent := f.payment(t, f.genesis, nil, 1)
	pid := parent.Certificate.Summary.OutputID(0)
	child := f.payment(t, pid, &parent.Certificate, 2)
	f.settle(t, child, 100)
	repair := func(now int64) error {
		return f.db.Update(func(v state.ReadView) ([]state.Change, error) {
			tr, err := EvaluateDirectCompensation(v, pid, f.policy, now)
			return tr.Changes, err
		})
	}
	if err := repair(129); err == nil {
		t.Fatal("paid before deadline")
	}
	if err := repair(130); err != nil {
		t.Fatal(err)
	}
	if err := repair(131); err != nil {
		t.Fatal(err)
	}
	if balance := loadDirect[uint64](t, f.db, AccountKey(f.org.Org, protocol.AssetCAL)); balance != 9999900 {
		t.Fatalf("duplicate/missing debit: %d", balance)
	}
	f.settle(t, parent, 132)
	if got := loadDirect[state.Creation](t, f.db, DirectCreationKey(pid, 1)); !got.Final {
		t.Fatal("late output not available")
	}
	if got := loadDirect[protocol.CreditReceipt](t, f.db, DirectCALCreditKey(parent.Certificate.QC.Fact)); got.Paid != 100 || got.Discharged != 0 {
		t.Fatalf("paid principal returned as credit: %+v", got)
	}
	conflict := f.payment(t, pid, &parent.Certificate, 4)
	v, err := VerifyDirectPayment(conflict, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	err = f.db.Update(func(view state.ReadView) ([]state.Change, error) {
		tr, err := EvaluateDirectPayment(view, v, f.policy, 133)
		return tr.Changes, err
	})
	if err == nil {
		t.Fatal("old certificate spent again")
	}
	if len(f.settle(t, parent, 134).Changes) != 0 {
		t.Fatal("late parent created twice")
	}
}
