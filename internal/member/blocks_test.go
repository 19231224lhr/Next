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
		name                     string
		repair, split, sameBlock bool
	}{{"source_arrives", false, false, false}, {"repair_then_late", true, false, false}, {"partial_repair_then_late", true, true, false}, {"child_parent_same_block", false, false, true}} {
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
			db := store.NewMemory()
			defer db.Close()
			m, err := member.New(member.Config{Organization: f.Org, Index: 0, Key: f.Keys[0], Peers: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Workers: 1, Direct: &settings}, db, f.Genesis)
			if err != nil {
				t.Fatal(err)
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
			pc, err := f.DirectCertificate(parent, policy)
			if err != nil {
				t.Fatal(err)
			}
			body := parent.Body
			body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: pc.Summary.OutputID(0), Evidence: protocol.Hash(pc.QC.Fact)}}
			body.Outputs = []protocol.Output{parent.Body.Outputs[0]}
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
			approved := make(map[protocol.SpendFactID][]byte)
			for _, p := range []protocol.DirectPayment{pp, cp} {
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
				apply(raw, tr)
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
