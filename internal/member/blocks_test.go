package member_test

import (
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

func TestBlockFollowerMissingRepairLateAndDuplicate(t *testing.T) {
	for _, repair := range []bool{false, true} {
		t.Run(map[bool]string{false: "source_arrives", true: "repair_then_late"}[repair], func(t *testing.T) {
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
			pc, err := f.DirectCertificate(parent, policy)
			if err != nil {
				t.Fatal(err)
			}
			body := parent.Body
			body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: pc.Summary.OutputID(0), Evidence: protocol.Hash(pc.QC.Fact)}}
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
			for _, p := range []protocol.DirectPayment{pp, cp} {
				if _, err = m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: p.Tx, InputCertificates: p.InputCertificates}); err != nil {
					t.Fatal(err)
				}
			}
			var previous []byte
			height := int64(0)
			apply := func(raw []byte, tr state.Transition) {
				t.Helper()
				height++
				trust, data := testkit.Block("follow", height, previous, [][]byte{raw}, []*abci.ExecTxResult{{Data: tr.Data}})
				b, err := finality.VerifyBlock(trust, data)
				if err != nil {
					t.Fatal(err)
				}
				if err = blockfollow.Commit(db, b, m.ApplyBlock); err != nil {
					t.Fatal(err)
				}
				if err = blockfollow.Commit(wdb, b, w.ApplyBlock); err != nil {
					t.Fatal(err)
				}
				// Same block is harmless; cursor and quota share the durable transaction.
				if err = blockfollow.Commit(db, b, m.ApplyBlock); err != nil {
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
				raw, err := p.MarshalBinary()
				if err != nil {
					t.Fatal(err)
				}
				apply(raw, tr)
			}
			settle(cp, 100)
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
				raw, _ := cp.MarshalBinary()
				command := protocol.RepairInput{Network: f.Org.Network, Output: pc.Summary.OutputID(0), Height: 1, TransactionBytes: raw, Parts: []chameleon.Opening{{}}}
				raw, err = command.MarshalBinary()
				if err != nil {
					t.Fatal(err)
				}
				apply(raw, tr)
			}
			settle(pp, 132)
			status, err = m.DirectStatus(cc.QC.Fact)
			if err != nil || !status.Closed {
				t.Fatal("child fee not closed", status, err)
			}
			cal, err := m.Quota(f.Genesis.Grants[0].Key, 0)
			if err != nil {
				t.Fatal(err)
			}
			expected := uint64(0)
			if repair {
				expected = 100
			}
			if cal.Reserved != expected {
				t.Fatalf("CAL residual=%d expected=%d", cal.Reserved, expected)
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
				a, _, err := state.Load[state.Approval](v, state.Key(state.KeyApproval, cc.QC.Fact[:]))
				if err != nil {
					return err
				}
				p, _, err := state.Load[member.LocalProgress](v, member.ProgressKey(cc.QC.Fact))
				if err != nil {
					return err
				}
				for _, d := range a.Debits {
					if d.Key.Kind == protocol.ResourceFUEL && d.Cap-d.Applied != p.Fee.Rewards+p.Fee.Burned {
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
