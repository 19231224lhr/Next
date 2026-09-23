package member_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	abci "github.com/cometbft/cometbft/abci/types"
	"os"
	"testing"
	"utxo/crypto/chameleon"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/committee"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type countedBlockStore struct {
	store.Store
	updates int
}

func (s *countedBlockStore) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	s.updates++
	return s.Store.Update(fn)
}

func TestInvalidPaymentRejectedBeforeMemberWrite(t *testing.T) {
	f := testkit.NewFixture("prepare", "org", 1)
	f.EnableDirect()
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	pemblock, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(pemblock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	settings := rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	db := &countedBlockStore{Store: store.NewMemory()}
	defer db.Close()
	m, err := member.New(member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Peers: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Workers: 1, Direct: &settings}, db, f.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := (protocol.ExecutionResult{Applied: true}).MarshalBinary()
	trust, data := testkit.Block("prepare", 1, nil, [][]byte{{0xff}}, []*abci.ExecTxResult{{Data: result}})
	b, err := finality.VerifyBlock(trust, data)
	if err != nil {
		t.Fatal(err)
	}
	db.updates = 0
	if err := blockfollow.Commit(db, b, m.PrepareBlock); err == nil {
		t.Fatal("invalid payment accepted")
	}
	if db.updates != 0 {
		t.Fatalf("static decoding entered %d write transactions", db.updates)
	}
}

func TestBlockFollowerMissingRepairLateAndDuplicate(t *testing.T) {
	for _, scenario := range []struct {
		name                                   string
		repair, split, sameBlock, owner, multi bool
	}{{"source_arrives", false, false, false, false, false}, {"repair_then_late", true, false, false, false, false}, {"partial_repair_then_late", true, true, false, false, false}, {"child_parent_same_block", false, false, true, false, false}, {"owner_source", false, false, false, true, false}, {"owner_repair", true, false, false, true, false}, {"owner_same_block", false, false, true, true, false}, {"owner_multiple_refunds", false, true, false, true, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			repair := scenario.repair
			f := testkit.NewFixture("follow", "org", 1)
			f.EnableDirect()
			raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
			if err != nil {
				t.Fatal(err)
			}
			pemblock, _ := pem.Decode(raw)
			key, err := x509.ParsePKCS1PrivateKey(pemblock.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			settings := rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
			policy, err := settings.Policy(f.Schedule, []protocol.OrgConfig{f.Org})
			if err != nil {
				t.Fatal(err)
			}
			var feeOrigins [3]state.OriginOutput
			if scenario.owner {
				for i := range feeOrigins {
					id := protocol.OutputID(protocol.Digest("owner-fee", []byte{byte(i)}))
					out := f.Genesis.Outputs[0].Output
					out.Asset = protocol.AssetFUEL
					out.Amount = 1500
					feeOrigins[i] = state.OriginOutput{ID: id, Fact: protocol.Digest("fee-genesis", id[:]), Output: out}
					f.Genesis.Outputs = append(f.Genesis.Outputs, feeOrigins[i])
				}
			}
			db := store.NewMemory()
			defer db.Close()
			m, err := member.New(member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Peers: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Workers: 1, Direct: &settings}, db, f.Genesis)
			if err != nil {
				t.Fatal(err)
			}
			var observer *member.Member
			var observerDB store.Store
			if scenario.owner {
				observerDB = store.NewMemory()
				defer observerDB.Close()
				observer, err = member.New(member.Config{Organization: f.Org, Index: 1, Key: f.Keys[1], Peers: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Workers: 1, Direct: &settings}, observerDB, f.Genesis)
				if err != nil {
					t.Fatal(err)
				}
			}
			wdb := store.NewMemory()
			defer wdb.Close()
			w, err := wallet.New(wdb, f.Org.Network, f.Genesis.Outputs[0].Output.Recipient.Owner, []protocol.OrgConfig{f.Org})
			if err != nil {
				t.Fatal(err)
			}
			ledger := store.NewMemory()
			defer ledger.Close()
			_, err = committee.NewEngine(committee.EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Direct: &settings, Accounts: []committee.GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetCAL, Balance: 1000000000}, {Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1000000000}}}, ledger)
			if err != nil {
				t.Fatal(err)
			}
			parent, err := f.FastTransaction(0, 1, policy)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.split {
				body := parent.Body
				body.Outputs = []protocol.Output{body.Outputs[0], body.Outputs[0]}
				body.Outputs[0].Amount, body.Outputs[1].Amount = 40, 60
				body.Intent = body.IntentID()
				parent, err = protocol.NewFastTx(body, parent.Claims, policy.Key)
				if err != nil {
					t.Fatal(err)
				}
				parent.Auth = []protocol.OwnerAuth{protocol.SignOwner(parent.ID(), f.Owner)}
			}
			if scenario.owner {
				body := parent.Body
				body.Fee = protocol.FeeTerms{Source: protocol.OwnerFinalUTXO, Maximum: 1000, Inputs: []protocol.Input{{Kind: protocol.FinalInput, Output: feeOrigins[0].ID, Evidence: feeOrigins[0].Fact}}, Refund: feeOrigins[0].Output.Recipient}
				body.Admission = nil
				for _, g := range f.Genesis.Grants {
					if g.Key.Kind != protocol.ResourceFUEL && g.Key.Kind != protocol.ResourcePolicy {
						body.Admission = append(body.Admission, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
					}
				}
				body.Intent = body.IntentID()
				parent, err = protocol.NewFastTx(body, parent.Claims, policy.Key)
				if err != nil {
					t.Fatal(err)
				}
				parent.Auth = []protocol.OwnerAuth{protocol.SignOwner(parent.ID(), f.Owner)}
			}
			pc, err := f.DirectCertificate(parent, policy)
			if err != nil {
				t.Fatal(err)
			}
			body := parent.Body
			body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: pc.Summary.OutputID(0), Evidence: protocol.Hash(pc.QC.Fact)}}
			body.Outputs = []protocol.Output{parent.Body.Outputs[0]}
			if scenario.owner {
				body.Fee.Inputs = []protocol.Input{{Kind: protocol.FinalInput, Output: feeOrigins[1].ID, Evidence: feeOrigins[1].Fact}}
			}
			body.Nonce[0] = 42
			body.Intent = body.IntentID()
			child, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: parent.Body.Outputs[0]}}, policy.Key)
			if err != nil {
				t.Fatal(err)
			}
			child.Auth = []protocol.OwnerAuth{protocol.SignOwner(child.ID(), f.Owner)}
			cc, err := f.DirectCertificate(child, policy)
			if err != nil {
				t.Fatal(err)
			}
			pp := protocol.DirectPayment{Tx: parent, Certificate: pc}
			cp := protocol.DirectPayment{Tx: child, Certificate: cc, InputCertificates: []protocol.InputCertificate{{Certificate: pc}}}
			var other protocol.DirectPayment
			if scenario.multi {
				b := child.Body
				b.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: pc.Summary.OutputID(1), Evidence: protocol.Hash(pc.QC.Fact)}}
				b.Outputs = []protocol.Output{parent.Body.Outputs[1]}
				b.Nonce[0] = 43
				b.Fee.Inputs = []protocol.Input{{Kind: protocol.FinalInput, Output: feeOrigins[2].ID, Evidence: feeOrigins[2].Fact}}
				b.Intent = b.IntentID()
				tx, e := protocol.NewFastTx(b, []protocol.InputClaim{{Output: b.Outputs[0]}}, policy.Key)
				if e != nil {
					t.Fatal(e)
				}
				tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.Owner)}
				cert, e := f.DirectCertificate(tx, policy)
				if e != nil {
					t.Fatal(e)
				}
				other = protocol.DirectPayment{Tx: tx, Certificate: cert, InputCertificates: []protocol.InputCertificate{{Certificate: pc, Index: 1}}}
			}
			approved := make(map[protocol.SpendFactID][]byte)
			payments := []protocol.DirectPayment{pp, cp}
			if scenario.multi {
				payments = append(payments, other)
			}
			for _, p := range payments {
				if scenario.owner && p.Tx.ID() == child.ID() {
					bad := child
					bad.Body.Fee.Inputs = append([]protocol.Input(nil), parent.Body.Fee.Inputs...)
					bad.Body.Intent = bad.Body.IntentID()
					bad, err = protocol.NewFastTx(bad.Body, bad.Claims, policy.Key)
					if err != nil {
						t.Fatal(err)
					}
					bad.Auth = []protocol.OwnerAuth{protocol.SignOwner(bad.ID(), f.Owner)}
					if _, err = m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: bad, InputCertificates: cp.InputCertificates}); err == nil {
						t.Fatal("shared fee input accepted")
					}
					// The valid child immediately afterward proves no CAL lock escaped the failed transaction.
				}

				if _, err = m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: p.Tx, InputCertificates: p.InputCertificates}); err != nil {
					t.Fatal(err)
				}
				if err = db.View(func(v state.ReadView) error {
					var err error
					approved[p.Certificate.QC.Fact], err = v.Get(state.Key(state.KeyApproval, p.Certificate.QC.Fact[:]))
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			var previous []byte
			height := int64(0)
			var blockTxs [][]byte
			var blockResults []*abci.ExecTxResult
			apply := func(raw []byte, tr state.Transition) {
				t.Helper()
				blockTxs = append(blockTxs, raw)
				blockResults = append(blockResults, &abci.ExecTxResult{Data: tr.Data})
				if scenario.sameBlock && len(blockTxs) < 2 {
					return
				}
				height++
				trust, data := testkit.Block("follow", height, previous, blockTxs, blockResults)
				blockTxs, blockResults = nil, nil
				b, err := finality.VerifyBlock(trust, data)
				if err != nil {
					t.Fatal(err)
				}
				if err = blockfollow.Commit(db, b, m.PrepareBlock); err != nil {
					t.Fatal(err)
				}
				if observer != nil {
					if err = blockfollow.Commit(observerDB, b, observer.PrepareBlock); err != nil {
						t.Fatal(err)
					}
				}
				if err = blockfollow.Commit(wdb, b, w.PrepareBlock); err != nil {
					t.Fatal(err)
				}
				// Same block is harmless; cursor and quota share the durable transaction.
				if err = blockfollow.Commit(db, b, m.PrepareBlock); err != nil {
					t.Fatal(err)
				}
				previous = b.Hash()
			}
			settle := func(p protocol.DirectPayment, now int64) {
				t.Helper()
				v, err := rules.VerifyDirectPayment(p, policy)
				if err != nil {
					t.Fatal(err)
				}
				var tr state.Transition
				err = ledger.Update(func(view state.ReadView) ([]state.Change, error) {
					var err error
					tr, err = rules.EvaluateDirectPayment(view, v, policy, now)
					return tr.Changes, err
				})
				if err != nil {
					t.Fatal(err)
				}
				raw, err := p.Submission().MarshalBinary()
				if err != nil {
					t.Fatal(err)
				}
				if scenario.multi && p.Tx.ID() == parent.ID() {
					r, e := protocol.DecodeExecution(tr.Data)
					if e != nil || len(r.FeeOutputs) != 4 {
						t.Fatalf("multiple child refunds lost: %d %v", len(r.FeeOutputs), e)
					}
				}
				apply(raw, tr)
			}
			if scenario.multi {
				settle(other, 100)
			}
			settle(cp, 100)
			if !scenario.sameBlock {
				if ok, err := w.DirectFinal(cc.Summary.OutputID(0), 0); err != nil || !ok {
					t.Fatal("child wallet still waits for source", err)
				}
				status, err := m.DirectStatus(cc.QC.Fact)
				if err != nil || !status.Observed || status.Closed {
					t.Fatal("missing-input fee closed early", status, err)
				}
				if repair {
					var tr state.Transition
					err = ledger.Update(func(v state.ReadView) ([]state.Change, error) {
						var err error
						tr, err = rules.EvaluateDirectCompensation(v, pc.Summary.OutputID(0), policy, 131)
						return tr.Changes, err
					})
					if err != nil {
						t.Fatal(err)
					}
					raw, _ := cp.Submission().MarshalBinary()
					command := protocol.RepairInput{Network: f.Org.Network, Output: pc.Summary.OutputID(0), Height: 1, TransactionBytes: raw, Parts: []chameleon.Opening{{}}}
					raw, err = command.MarshalBinary()
					if err != nil {
						t.Fatal(err)
					}
					apply(raw, tr)
				}
			}
			settle(pp, 132)
			if scenario.owner {
				refund := protocol.OutputIdentity(f.Org.Network, child.ID(), protocol.FeeRefundIndex)
				if ok, err := w.DirectFinal(refund, 0); err != nil || !ok {
					t.Fatal("wallet missed final FUEL refund", err)
				}
				expected := uint64(906)
				if repair {
					expected = 901
				}
				for _, db := range []store.Store{db, ledger, observerDB} {
					if err := db.View(func(v state.ReadView) error {
						c, ok, e := state.Load[state.Creation](v, rules.DirectCreationKey(refund, 0))
						if !ok || c.Output.Asset != protocol.AssetFUEL || c.Output.Amount != expected {
							t.Fatalf("refund missing in ledger/member: %+v", c)
						}
						return e
					}); err != nil {
						t.Fatal(err)
					}
				}
			}

			if scenario.multi {
				id := protocol.OutputIdentity(f.Org.Network, other.Tx.ID(), protocol.FeeRefundIndex)
				if ok, e := w.DirectFinal(id, 0); e != nil || !ok {
					t.Fatal("other child wallet refund missing", e)
				}
				for _, target := range []store.Store{db, observerDB, ledger} {
					if e := target.View(func(v state.ReadView) error {
						c, ok, e := state.Load[state.Creation](v, rules.DirectCreationKey(id, 0))
						if !ok || c.Output.Amount != 906 {
							t.Fatal("other child refund identity lost")
						}
						return e
					}); e != nil {
						t.Fatal(e)
					}
				}
			}
			status, err := m.DirectStatus(cc.QC.Fact)
			if err != nil || !status.Closed {
				t.Fatal("child fee not closed", status, err)
			}
			cal, err := m.Quota(f.Genesis.Grants[0].Key, 0)
			if err != nil {
				t.Fatal(err)
			}
			expected := uint64(0)
			if repair {
				expected = parent.Body.Outputs[0].Amount
			}
			if cal.Reserved != expected {
				t.Fatalf("CAL residual=%d expected=%d", cal.Reserved, expected)
			}
			// Public settlement preceded any INSTALL; late replication must not
			// reopen the outbox or change the already applied budget.
			if err = m.InstallDirect(pp); err != nil {
				t.Fatal(err)
			}
			after, err := m.Quota(f.Genesis.Grants[0].Key, 0)
			if err != nil || after.Reserved != expected {
				t.Fatal("late INSTALL changed quota", after, err)
			}
			if err = db.View(func(v state.ReadView) error {
				_, found, err := state.Load[state.Outbox](v, state.Key(state.KeyOutbox, pc.QC.Fact[:]))
				if found {
					t.Fatal("late INSTALL reopened outbox")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			instance := uint8(0)
			if repair {
				instance = 1
			}
			if ok, err := w.DirectFinal(pc.Summary.OutputID(0), instance); err != nil || !ok {
				t.Fatal("wrong source output instance", err)
			}
			// A fast certificate arriving after its block must not overwrite finality.
			if err = w.ReceiveDirect(parent.Body.Outputs[0], pc, 0); err != nil {
				t.Fatal(err)
			}
			err = db.View(func(v state.ReadView) error {
				for fact, original := range approved {
					current, err := v.Get(state.Key(state.KeyApproval, fact[:]))
					if err != nil {
						return err
					}
					if !bytes.Equal(original, current) {
						t.Fatal("settlement rewrote immutable approval")
					}
				}
				a, _, err := state.Load[state.Approval](v, state.Key(state.KeyApproval, cc.QC.Fact[:]))
				if err != nil {
					return err
				}
				p, _, err := state.Load[member.LocalProgress](v, member.ProgressKey(cc.QC.Fact))
				if err != nil {
					return err
				}
				applied, err := member.AppliedDebits(v, a)
				if err != nil {
					return err
				}
				for i, d := range a.Debits {
					if d.Key.Kind == protocol.ResourceFUEL && d.Cap-applied[i] != p.Fee.Rewards+p.Fee.Burned {
						t.Fatal("real fees were recycled")
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
