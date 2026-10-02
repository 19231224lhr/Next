package member_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"utxo/internal/committee"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

// Intent is global per subject, whereas approval state belongs to an
// organization. Record the actual boundary using real member approvals:
// two independent inputs do not make reuse of one subject's nonce valid.
func TestDirectIntentScopeAcrossOrganizations(t *testing.T) {
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	settings := rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	for _, scenario := range []struct {
		name               string
		sameOwner, reverse bool
	}{
		{name: "different_subjects"},
		{name: "same_subject_a_first", sameOwner: true},
		{name: "same_subject_b_first", sameOwner: true, reverse: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			sameOwner := scenario.sameOwner
			a, b := testkit.NewFixture("intent-scope", "a", 1), testkit.NewFixture("intent-scope", "b", 1)
			if sameOwner {
				b.Owner = a.Owner
				b.Genesis.Outputs[0].Output.Recipient = protocol.NewDescriptor(b.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: b.Org.Org}, b.Owner)
				for i := range b.Genesis.Grants {
					b.Genesis.Grants[i].Subject = b.Genesis.Outputs[0].Output.Recipient.Owner
				}
			}
			a.EnableDirect()
			b.EnableDirect()
			orgs := []protocol.OrgConfig{a.Org, b.Org}
			policy, err := settings.Policy(a.Schedule, orgs)
			if err != nil {
				t.Fatal(err)
			}
			genesis := a.Genesis
			genesis.Outputs = append(genesis.Outputs, b.Genesis.Outputs...)
			genesis.Grants = append(genesis.Grants, b.Genesis.Grants...)
			ledger := store.NewMemory()
			t.Cleanup(func() { ledger.Close() })
			var accounts []committee.GenesisAccount
			for _, org := range orgs {
				for _, asset := range []protocol.Asset{protocol.AssetCAL, protocol.AssetFUEL} {
					accounts = append(accounts, committee.GenesisAccount{Owner: org.Org, Asset: asset, Balance: 1_000_000_000})
				}
			}
			engine, err := committee.NewEngine(committee.EngineConfig{Network: a.Org.Network, Organizations: orgs, Schedule: a.Schedule, Genesis: genesis, Direct: &settings, Accounts: accounts}, ledger)
			if err != nil {
				t.Fatal(err)
			}
			var payments []protocol.DirectPayment
			var signers [2][]*member.Member
			for orgIndex, f := range []testkit.Fixture{a, b} {
				tx, err := f.FastTransaction(0, 1, policy)
				if err != nil {
					t.Fatal(err)
				}
				certificate := protocol.OutputCertificate{}
				for i := 0; i < 3; i++ {
					db := store.NewMemory()
					t.Cleanup(func() { db.Close() })
					m, err := member.New(member.Config{Organization: f.Org, Index: uint16(i), Key: f.Keys[i], Peers: orgs, Schedule: f.Schedule, Workers: 1, Direct: &settings}, db, genesis)
					if err != nil {
						t.Fatal(err)
					}
					if !sameOwner && f.Org.Org == b.Org.Org {
						body := tx.Body
						body.Subject = a.Genesis.Outputs[0].Output.Recipient.Owner
						body.Intent = body.IntentID()
						forged, err := protocol.NewFastTx(body, tx.Claims, policy.Key)
						if err != nil {
							t.Fatal(err)
						}
						forged.Auth = []protocol.OwnerAuth{protocol.SignOwner(forged.ID(), b.Owner)}
						if _, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: forged}); err == nil {
							t.Fatal("foreign subject impersonation approved")
						}
					}
					vote, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx})
					if err != nil {
						t.Fatal(err)
					}
					certificate.Summary, certificate.QC.Fact = vote.Summary, vote.Summary.Fact()
					certificate.QC.Votes = append(certificate.QC.Votes, vote.Vote)
					signers[orgIndex] = append(signers[orgIndex], m)
				}
				if err := certificate.Verify(f.Org); err != nil {
					t.Fatal(err)
				}
				payments = append(payments, protocol.DirectPayment{Tx: tx, Certificate: certificate})
			}
			if (payments[0].Tx.Body.Intent == payments[1].Tx.Body.Intent) != sameOwner {
				t.Fatal("unexpected subject domain separation")
			}
			if payments[0].Tx.Body.Inputs[0].Output == payments[1].Tx.Body.Inputs[0].Output || payments[0].Tx.Body.Fee.Account == payments[1].Tx.Body.Fee.Account {
				t.Fatal("scenario must use independent principal and fee funding")
			}
			first, second := 0, 1
			if scenario.reverse {
				first, second = second, first
			}
			execute := func(payment protocol.DirectPayment, height int64) error {
				raw, err := payment.Submission().MarshalBinary()
				if err != nil {
					return err
				}
				return ledger.Update(func(v state.ReadView) ([]state.Change, error) {
					tr, err := engine.ExecuteAt(v, raw, committee.BlockContext{Height: height, Time: time.Unix(100, 0)})
					return tr.Changes, err
				})
			}
			if err := execute(payments[first], 1); err != nil {
				t.Fatal(err)
			}
			before, err := store.Scan(ledger, nil, nil, 1000)
			if err != nil || len(before) == 0 || len(before) == 1000 {
				t.Fatal(err)
			}
			err = execute(payments[second], 2)
			if !sameOwner {
				if err != nil {
					t.Fatal("unrelated subject was blocked", err)
				}
				return
			}
			if !errors.Is(err, rules.ErrConflict) {
				t.Fatalf("expected global intent conflict, got %v", err)
			}
			if err := execute(payments[second], 3); !errors.Is(err, rules.ErrConflict) {
				t.Fatalf("resubmission bypassed conflict: %v", err)
			}
			after, err := store.Scan(ledger, nil, nil, 1000)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected payment changed public state", err)
			}
			fixture := []testkit.Fixture{a, b}[second]
			for _, m := range signers[second] {
				quota, err := m.Quota(fixture.Genesis.Grants[0].Key, 0)
				if err != nil || quota.Reserved != 100 {
					t.Fatal("rejected source lost its original CAL reservation", err)
				}
				conflicting, err := fixture.FastTransaction(0, 2, policy)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: conflicting}); !errors.Is(err, rules.ErrConflict) {
					t.Fatal("changing nonce released the original input lock", err)
				}
			}
		})
	}
}
