package member

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"os"
	"reflect"
	"testing"

	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type approvalReadView struct {
	state.ReadView
	key   []byte
	reads *int
}

func (v approvalReadView) Get(key []byte) ([]byte, error) {
	if bytes.Equal(key, v.key) {
		*v.reads++
	}
	return v.ReadView.Get(key)
}

type approvalFixture struct {
	Org      protocol.OrgConfig
	Keys     [4]ed25519.PrivateKey
	Owner    ed25519.PrivateKey
	Genesis  state.Genesis
	Schedule rules.Schedule
}

func approvalReuseFixture(t testing.TB, c3 bool) (*Member, store.Store, approvalFixture, rules.DirectPolicy) {
	t.Helper()
	f := approvalFixture{Schedule: rules.DefaultSchedule()}
	f.Org = protocol.OrgConfig{Network: protocol.Digest("approval-reuse"), Org: protocol.Digest("reuse-org"), Epoch: 1}
	for i := range f.Keys {
		seed := make([]byte, ed25519.SeedSize)
		seed[0] = byte(i + 1)
		f.Keys[i] = ed25519.NewKeyFromSeed(seed)
		copy(f.Org.Members[i][:], f.Keys[i].Public().(ed25519.PublicKey))
	}
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 10
	f.Owner = ed25519.NewKeyFromSeed(seed)
	out := protocol.Output{Asset: protocol.AssetCAL, Amount: 100, Recipient: protocol.NewDescriptor(f.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: f.Org.Org}, f.Owner)}
	f.Genesis = state.Genesis{Network: f.Org.Network, Outputs: []state.OriginOutput{{ID: protocol.OutputID(protocol.Digest("reuse-origin")), Fact: protocol.Digest("reuse-final"), Output: out}}}
	for _, kind := range []protocol.ResourceKind{protocol.ResourceCAL, protocol.ResourceFUEL, protocol.ResourceExecution, protocol.ResourceBytes, protocol.ResourcePolicy} {
		account := f.Org.Org
		if kind == protocol.ResourcePolicy {
			account = protocol.Digest("reuse-owner-policy")
		}
		f.Genesis.Grants = append(f.Genesis.Grants, state.Grant{ID: protocol.Digest("reuse-grants", []byte{byte(kind)}), Organization: f.Org.Hash(), Key: protocol.ResourceKey{Kind: kind, Account: account, Version: 1}, Amount: 1000000000, Subject: out.Recipient.Owner})
	}
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	settings := rules.DirectSettings{Modulus: k.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	if c3 {
		pool := protocol.Digest("reuse-pool")
		ref := protocol.AdmissionRef{Key: protocol.ResourceKey{Kind: protocol.ResourceFUEL, Account: pool, Version: 1}, Grant: protocol.Digest("reuse-grant")}
		settings.C3 = &rules.C3Settings{Current: protocol.Digest("reuse-policy"), Policies: []rules.C3Policy{{ID: protocol.Digest("reuse-policy"), Schedule: protocol.FeeSchedule{AttachReward: 1, ResolveReward: 1, Open: 1, Close: 1, Compensate: 1, Repay: 1}}}, Issuers: []rules.C3Issuer{{Config: f.Org.Hash(), Funding: ref, Revenue: protocol.Digest("reuse-revenue"), Beneficiary: protocol.Digest("reuse-beneficiary")}}}
		f.Genesis.Grants = append(f.Genesis.Grants, state.Grant{ID: ref.Grant, Key: ref.Key, Organization: f.Org.Hash(), Amount: 1000000000})
	}
	p, err := settings.Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if err != nil {
		t.Fatal(err)
	}
	db := store.NewMemory()
	t.Cleanup(func() { db.Close() })
	m, err := New(Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Peers: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Workers: 1, Direct: &settings}, db, f.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	return m, db, f, p
}

func approvalReusePayment(t testing.TB, f approvalFixture, p rules.DirectPolicy) protocol.DirectSubmission {
	t.Helper()
	origin := f.Genesis.Outputs[0]
	body := protocol.TxBody{Wire: protocol.DirectWireVersion, Version: protocol.DirectVersion, Network: f.Org.Network, Kind: protocol.FastTransfer, Subject: origin.Output.Recipient.Owner, Certifier: f.Org.Org, Config: f.Org.Hash(), Epoch: 1, Rules: p.Rules(), Inputs: []protocol.Input{{Kind: protocol.FinalInput, Output: origin.ID, Evidence: origin.Fact}}, Outputs: []protocol.Output{origin.Output}, Fee: protocol.FeeTerms{Source: protocol.OrgReserve, Account: f.Org.Org, Policy: protocol.Digest("reuse-owner-policy"), Version: 1, Maximum: 1000}, Work: protocol.WorkLimit{Execution: 100000, Bytes: 10000000, Depth: 1, Ancestors: 1}}
	body.Nonce[0] = 1
	var refs []protocol.AdmissionRef
	for _, g := range f.Genesis.Grants {
		refs = append(refs, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
	}
	body.Admission = refs
	if p.C3 != nil {
		if err := rules.BindC3(&body, p, refs); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: origin.Output}}, p.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.Owner)}
	vector, err := rules.PrepareDirectVector(tx, p)
	if err != nil {
		t.Fatal(err)
	}
	c := protocol.OutputCertificate{Summary: protocol.SummaryFor(tx, vector)}
	c.QC.Fact = c.Summary.Fact()
	for i := 0; i < 3; i++ {
		c.QC.Votes = append(c.QC.Votes, protocol.SignSpend(c.QC.Fact, uint16(i), f.Keys[i]))
	}
	return (protocol.DirectPayment{Tx: tx, Certificate: c}).Submission()
}

func applyApprovalReuse(t *testing.T, m *Member, db store.Store, pay protocol.DirectSubmission, result protocol.ExecutionResult) (int, error) {
	t.Helper()
	reads := 0
	err := db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(approvalReadView{ReadView: v, key: state.Key(state.KeyApproval, pay.Authorization.Fact[:]), reads: &reads})
		if err := m.applyPayment(o, pay, result, 1); err != nil {
			return nil, err
		}
		return o.Changes(), nil
	})
	return reads, err
}

func TestC3ApplyPaymentReadsSelfApprovalOnce(t *testing.T) {
	for _, flow := range []string{"source_first", "opened_source", "compensated_late"} {
		t.Run(flow, func(t *testing.T) {
			m, db, f, p := approvalReuseFixture(t, true)
			pay := approvalReusePayment(t, f, p)
			if _, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: pay.Tx}); err != nil {
				t.Fatal(err)
			}
			result := protocol.ExecutionResult{Applied: true}
			if flow != "source_first" {
				if err := db.Update(func(v state.ReadView) ([]state.Change, error) {
					o := state.NewOverlay(v)
					if err := m.c3Event(o, pay.Summary(), rules.C3Open, 0); err != nil {
						return nil, err
					}
					if flow == "compensated_late" {
						if err := m.c3Event(o, pay.Summary(), rules.C3Compensate, 0); err != nil {
							return nil, err
						}
						progress, _, err := state.Load[LocalProgress](o, ProgressKey(pay.Authorization.Fact))
						if err != nil {
							return nil, err
						}
						progress.Paid = pay.Tx.Body.Outputs[0].Amount
						if err := state.Put(o, ProgressKey(pay.Authorization.Fact), progress); err != nil {
							return nil, err
						}
					}
					return o.Changes(), nil
				}); err != nil {
					t.Fatal(err)
				}
				if flow == "compensated_late" {
					result.RecoveredOutputs = []uint32{0}
				}
			}
			reads, err := applyApprovalReuse(t, m, db, pay, result)
			if err != nil {
				t.Fatal(err)
			}
			if reads != 1 {
				t.Fatalf("self approval decoded %d times, want once", reads)
			}
			if err := db.View(func(v state.ReadView) error {
				progress, found, err := state.Load[LocalProgress](v, ProgressKey(pay.Authorization.Fact))
				if err != nil {
					return err
				}
				if !found || !progress.Settled || !progress.Fee.Closed || progress.Pending != 0 || progress.C3 == nil || progress.C3.Held != 0 {
					t.Fatal("settlement did not close", progress)
				}
				if flow == "compensated_late" && progress.Recovered != progress.Paid {
					t.Fatal("paid loss not recovered")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestApplyPaymentApprovalReuseMissingAndLegacy(t *testing.T) {
	for _, c3 := range []bool{false, true} {
		for _, approved := range []bool{false, true} {
			m, db, f, p := approvalReuseFixture(t, c3)
			pay := approvalReusePayment(t, f, p)
			if approved {
				if _, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: pay.Tx}); err != nil {
					t.Fatal(err)
				}
			}
			reads, err := applyApprovalReuse(t, m, db, pay, protocol.ExecutionResult{Applied: true})
			if err != nil {
				t.Fatal(err)
			}
			if reads != 1 {
				t.Fatalf("c3=%v approved=%v reads=%d", c3, approved, reads)
			}
		}
	}
}

func TestC3MalformedApprovalRollbackAndErrorOrder(t *testing.T) {
	m, db, f, p := approvalReuseFixture(t, true)
	pay := approvalReusePayment(t, f, p)
	if err := db.Update(func(v state.ReadView) ([]state.Change, error) {
		return []state.Change{{Key: state.Key(state.KeyApproval, pay.Authorization.Fact[:]), Value: []byte("{")}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := store.Scan(db, nil, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	_, err = applyApprovalReuse(t, m, db, pay, protocol.ExecutionResult{Applied: true, RecoveredOutputs: []uint32{1}})
	if err == nil || err == protocol.ErrRule {
		t.Fatalf("malformed approval must fail before recovered-index validation: %v", err)
	}
	after, scanErr := store.Scan(db, nil, nil, 1000)
	if scanErr != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed application changed state", scanErr)
	}
}

func TestC3ApprovalReuseKeepsAuthorizationFactLookup(t *testing.T) {
	m, db, f, p := approvalReuseFixture(t, true)
	pay := approvalReusePayment(t, f, p)
	if _, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: pay.Tx}); err != nil {
		t.Fatal(err)
	}
	summaryFact := pay.Summary().Fact()
	pay.Authorization.Fact = protocol.SpendFactID(protocol.Digest("different-authorization-fact"))
	reads, err := applyApprovalReuse(t, m, db, pay, protocol.ExecutionResult{Applied: true})
	if err != rules.ErrAccounting {
		t.Fatalf("divergent authorization changed original accounting rejection: %v", err)
	}
	if reads != 1 {
		t.Fatalf("divergent authorization lookup skipped: %d reads", reads)
	}
	if err := db.View(func(v state.ReadView) error {
		progress, _, err := state.Load[LocalProgress](v, ProgressKey(summaryFact))
		if err != nil {
			return err
		}
		if progress.Settled {
			t.Fatal("summary approval reused for a different authorization fact")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Benchmark the real atomic evaluation against the unchanged approved snapshot.
// Each iteration discards a fresh overlay; fixture creation, reservation and
// committed writes are outside the timer. This is not an end-to-end benchmark.
func BenchmarkC3ApplyPaymentSelfApproval(b *testing.B) {
	m, db, f, p := approvalReuseFixture(b, true)
	pay := approvalReusePayment(b, f, p)
	if _, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: pay.Tx}); err != nil {
		b.Fatal(err)
	}
	result := protocol.ExecutionResult{Applied: true}
	b.ReportAllocs()
	if err := db.View(func(v state.ReadView) error {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			o := state.NewOverlay(v)
			if err := m.applyPayment(o, pay, result, 1); err != nil {
				return err
			}
		}
		b.StopTimer()
		return nil
	}); err != nil {
		b.Fatal(err)
	}
}

func TestC3ApprovalReuseReturnsActualLookupFact(t *testing.T) {
	m, db, f, p := approvalReuseFixture(t, true)
	pay := approvalReusePayment(t, f, p)
	if _, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: pay.Tx}); err != nil {
		t.Fatal(err)
	}
	want := pay.Summary().Fact()
	if err := db.View(func(v state.ReadView) error {
		o := state.NewOverlay(v)
		stored, found, err := state.Load[state.Approval](o, state.Key(state.KeyApproval, want[:]))
		if err != nil || !found {
			t.Fatal("missing fixture approval", err)
		}
		// The returned key must describe the actual lookup, even if a retained
		// record's own Fact field differs. Never use that field as the guard.
		stored.Fact = protocol.SpendFactID(protocol.Digest("different-record-fact"))
		if err := state.Put(o, state.Key(state.KeyApproval, want[:]), stored); err != nil {
			return err
		}
		a, found, lookupFact, err := m.c3EventWithApproval(o, pay.Summary(), rules.C3Execute, rules.C3WholeTransaction)
		if err != nil {
			return err
		}
		if !found || lookupFact != want || a.Fact == lookupFact {
			t.Fatal("helper did not retain actual lookup key")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
