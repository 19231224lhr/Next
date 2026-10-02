package member_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"reflect"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/committee"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestExposedSourceReconstructionAcrossOrganizationsAndDepth(t *testing.T) {
	f, other := testkit.NewFixture("exposed", "original", 1), testkit.NewFixture("exposed", "successor", 1)
	f.EnableDirect()
	other.EnableDirect()
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	settings := rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	orgs := []protocol.OrgConfig{f.Org, other.Org}
	p, err := settings.Policy(f.Schedule, orgs)
	if err != nil {
		t.Fatal(err)
	}
	g := f.Genesis
	g.Outputs = append(g.Outputs, other.Genesis.Outputs...)
	g.Grants = append(g.Grants, other.Genesis.Grants...)
	db, ledger := store.NewMemory(), store.NewMemory()
	defer db.Close()
	defer ledger.Close()
	m, err := member.New(member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Peers: orgs, Schedule: f.Schedule, Workers: 1, Direct: &settings}, db, g)
	if err != nil {
		t.Fatal(err)
	}
	accounts := []committee.GenesisAccount{}
	for _, org := range orgs {
		for _, asset := range []protocol.Asset{protocol.AssetCAL, protocol.AssetFUEL} {
			accounts = append(accounts, committee.GenesisAccount{Owner: org.Org, Asset: asset, Balance: 1_000_000_000})
		}
	}
	_, err = committee.NewEngine(committee.EngineConfig{Network: f.Org.Network, Organizations: orgs, Schedule: f.Schedule, Genesis: g, Direct: &settings, Accounts: accounts}, ledger)
	if err != nil {
		t.Fatal(err)
	}
	root, err := f.FastTransaction(0, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := f.DirectCertificate(root, p)
	if err != nil {
		t.Fatal(err)
	}
	body := root.Body
	body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: rc.Summary.OutputID(0), Evidence: protocol.Hash(rc.QC.Fact)}}
	body.Outputs = append([]protocol.Output(nil), root.Body.Outputs...)
	body.Outputs[0].Recipient = other.Genesis.Outputs[0].Output.Recipient
	body.Nonce[0] = 42
	body.Intent = body.IntentID()
	source, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: root.Body.Outputs[0]}}, p.Key)
	if err != nil {
		t.Fatal(err)
	}
	source.Auth = []protocol.OwnerAuth{protocol.SignOwner(source.ID(), f.Owner)}
	sc, err := f.DirectCertificate(source, p)
	if err != nil {
		t.Fatal(err)
	}
	parents := []protocol.InputCertificate{{Certificate: rc}}
	for _, req := range []protocol.DirectRequest{{Tx: root}, {Tx: source, InputCertificates: parents}} {
		if _, err = m.ApproveDirect(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	// No INSTALL for either source. Only the foreign successor is public.
	child, err := other.FastTransaction(0, 3, p)
	if err != nil {
		t.Fatal(err)
	}
	body = child.Body
	body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: sc.Summary.OutputID(0), Evidence: protocol.Hash(sc.QC.Fact)}}
	body.Intent = body.IntentID()
	child, err = protocol.NewFastTx(body, []protocol.InputClaim{{Output: source.Body.Outputs[0]}}, p.Key)
	if err != nil {
		t.Fatal(err)
	}
	child.Auth = []protocol.OwnerAuth{protocol.SignOwner(child.ID(), other.Owner)}
	cc, err := other.DirectCertificate(child, p)
	if err != nil {
		t.Fatal(err)
	}
	var previous []byte
	height := int64(0)
	settle := func(payment protocol.DirectPayment) {
		t.Helper()
		verified, e := rules.VerifyDirectPayment(payment, p)
		if e != nil {
			t.Fatal(e)
		}
		var tr state.Transition
		e = ledger.Update(func(v state.ReadView) ([]state.Change, error) {
			var e error
			tr, e = rules.EvaluateDirectPayment(v, verified, p, 100)
			return tr.Changes, e
		})
		if e != nil {
			t.Fatal(e)
		}
		wire, e := payment.Submission().MarshalBinary()
		if e != nil {
			t.Fatal(e)
		}
		height++
		trust, data := testkit.Block("exposed", height, previous, [][]byte{wire}, []*abci.ExecTxResult{{Data: tr.Data}})
		block, e := finality.VerifyBlock(trust, data)
		if e != nil {
			t.Fatal(e)
		}
		if e = blockfollow.Commit(db, block, m.PrepareBlock); e != nil {
			t.Fatal(e)
		}
		if e = blockfollow.Commit(db, block, m.PrepareBlock); e != nil {
			t.Fatal(e)
		}
		previous = block.Hash()
	}
	// Publication supplies the missing QC, not a new authorization or budget.
	approvalKey := state.Key(state.KeyApproval, sc.QC.Fact[:])
	var originalApproval []byte
	if err := db.View(func(v state.ReadView) error {
		var err error
		originalApproval, err = v.Get(approvalKey)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	quotaBefore, err := m.Quota(f.Genesis.Grants[0].Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	settle(protocol.DirectPayment{Tx: child, Certificate: cc, InputCertificates: []protocol.InputCertificate{{Certificate: sc}}})
	quotaAfter, err := m.Quota(f.Genesis.Grants[0].Key, 0)
	if err != nil || !reflect.DeepEqual(quotaBefore, quotaAfter) {
		t.Fatal("source reconstruction changed the original CAL reservation", err)
	}
	if err := db.View(func(v state.ReadView) error {
		current, err := v.Get(approvalKey)
		if !bytes.Equal(originalApproval, current) {
			t.Fatal("source reconstruction changed the original approval")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	load := func(fact protocol.SpendFactID) protocol.DirectPayment {
		t.Helper()
		var payment protocol.DirectPayment
		if e := db.View(func(v state.ReadView) error {
			pending, found, e := state.Load[state.Outbox](v, state.Key(state.KeyOutbox, fact[:]))
			if e != nil {
				return e
			}
			if !found {
				t.Fatal("missing reconstructed outbox")
			}
			payment, e = protocol.DecodeDirectPayment(pending.Certificate)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		return payment
	}
	rebuilt := load(sc.QC.Fact)
	expected, err := (protocol.DirectPayment{Tx: source, Certificate: sc, InputCertificates: parents}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	actual, err := rebuilt.MarshalBinary()
	if err != nil || !bytes.Equal(expected, actual) {
		t.Fatal("reconstruction changed the originally authorized payment bytes", err)
	}
	if len(rebuilt.InputCertificates) != 1 || rebuilt.InputCertificates[0].Certificate.QC.Fact != rc.QC.Fact {
		t.Fatal("reconstruction lost direct ancestor witness")
	}
	settle(rebuilt)
	settle(load(rc.QC.Fact))
	if e := db.View(func(v state.ReadView) error {
		for _, fact := range []protocol.SpendFactID{rc.QC.Fact, sc.QC.Fact} {
			_, found, e := state.Load[state.Outbox](v, state.Key(state.KeyOutbox, fact[:]))
			if e != nil {
				return e
			}
			if found {
				t.Fatal("settled source outbox survived")
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
