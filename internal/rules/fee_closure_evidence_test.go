package rules

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"utxo/internal/state"
	"utxo/protocol"
)

// Two certified inputs exercise both the per-input compensation budget and
// the single per-payment close. These are rules tests, not consensus tests.
func TestFeeClosureMultipleObligations(t *testing.T) {
	for _, ownerFee := range []bool{false, true} {
		for compensated := 0; compensated <= 2; compensated++ {
			for _, surplus := range []uint64{0, 17} {
				t.Run(fmt.Sprintf("owner=%v/paid=%d/surplus=%d", ownerFee, compensated, surplus), func(t *testing.T) {
					f := newDirectFixture(t)
					base := f.payment(t, f.genesis, nil, 1)
					body := base.Tx.Body
					half := f.output
					half.Amount = 50
					body.Outputs = []protocol.Output{half, half}
					makeTx := func(b protocol.TxBody, claims []protocol.InputClaim) protocol.FastTx {
						t.Helper()
						b.Intent = b.IntentID()
						tx, err := protocol.NewFastTx(b, claims, f.policy.Key)
						if err != nil {
							t.Fatal(err)
						}
						tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
						return tx
					}
					parent := f.certify(t, makeTx(body, base.Tx.Claims), nil)
					body = base.Tx.Body
					body.Nonce[0] = 2
					body.Inputs = nil
					var certs []InputCertificate
					for i := uint32(0); i < 2; i++ {
						body.Inputs = append(body.Inputs, protocol.Input{Kind: protocol.CertificateInput, Output: parent.Certificate.Summary.OutputID(i), Evidence: protocol.Hash(parent.Certificate.QC.Fact)})
						certs = append(certs, InputCertificate{Certificate: parent.Certificate, Index: i})
					}
					plan := f.policy.Base.Fee
					baseCost := plan.Register + plan.Settle + plan.Close
					minimum := baseCost + 2*f.policy.RepairCost
					body.Fee.Maximum = minimum + surplus
					if ownerFee {
						fee := seedOwnerFee(t, f, "two-obligations", 1500)
						body.Fee = protocol.FeeTerms{Source: protocol.OwnerFinalUTXO, Maximum: minimum + surplus, Inputs: []protocol.Input{fee}, Refund: f.output.Recipient}
						body.Admission = nil
						for _, g := range f.grants {
							if g.Key.Kind != protocol.ResourceFUEL && g.Key.Kind != protocol.ResourcePolicy {
								body.Admission = append(body.Admission, g)
							}
						}
					}
					claims := []protocol.InputClaim{{Output: half}, {Output: half}}
					sort.Slice(body.Inputs, func(i, j int) bool { return bytes.Compare(body.Inputs[i].Output[:], body.Inputs[j].Output[:]) < 0 })
					under := body
					under.Fee.Maximum = minimum - 1
					if _, err := PrepareDirectVector(makeTx(under, claims), f.policy); err != ErrLimited {
						t.Fatalf("underfunded admission: %v", err)
					}
					child := f.certify(t, makeTx(body, claims), certs)
					baseline := projectMoney(t, f.db)
					f.settle(t, child, 100)
					paymentKey := state.Key(keyDirectPayment, child.Certificate.QC.Fact[:])
					check := func(pending uint32, paidCount int) {
						t.Helper()
						p := loadDirect[directPayment](t, f.db, paymentKey)
						if p.Pending != pending || p.Fee.Closed != (pending == 0) || p.Fee.Validate() != nil {
							t.Fatalf("bad fee state: %+v", p)
						}
						if got := projectMoney(t, f.db); got != baseline {
							t.Fatalf("asset projection: %+v != %+v", got, baseline)
						}
						if pending == 0 {
							paid := baseCost + uint64(paidCount)*f.policy.RepairCost
							if p.Fee.Held != 0 || p.Fee.Rewards+p.Fee.Burned != paid || p.Fee.Refunded != body.Fee.Maximum-paid {
								t.Fatalf("wrong final fee: %+v", p.Fee)
							}
							for _, a := range child.Certificate.Summary.Admission {
								if a.Key.Kind == protocol.ResourceCAL {
									continue
								}
								u := loadDirect[PublicUsage](t, f.db, state.Key(state.KeyUsage, a.Key.Encode()))
								want := uint64(0)
								if a.Key.Kind == protocol.ResourceFUEL || a.Key.Kind == protocol.ResourcePolicy {
									want = paid
								}
								if u.Reserved != 0 || u.Spent != want {
									t.Fatalf("wrong closed usage: %+v", u)
								}
							}
						}
					}
					check(2, 0)
					for i := 0; i < compensated; i++ {
						id := parent.Certificate.Summary.OutputID(uint32(i))
						for repeat := 0; repeat < 2; repeat++ {
							err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
								tr, err := EvaluateDirectCompensation(v, id, f.policy, 130)
								if repeat == 1 && len(tr.Changes) != 0 {
									t.Fatal("duplicate compensation wrote state")
								}
								return tr.Changes, err
							})
							if err != nil {
								t.Fatal(err)
							}
						}
						check(uint32(1-i), i+1)
					}
					f.settle(t, parent, 131)
					p := loadDirect[directPayment](t, f.db, paymentKey)
					if p.Pending != 0 || !p.Fee.Closed || p.Fee.Held != 0 || p.Fee.Rewards+p.Fee.Burned != baseCost+uint64(compensated)*f.policy.RepairCost || p.Fee.Refunded != body.Fee.Maximum-baseCost-uint64(compensated)*f.policy.RepairCost || p.Fee.Validate() != nil {
						t.Fatalf("source closure: %+v", p)
					}
					// Parent fees legitimately add to shared Usage; the child itself
					// must remain byte-for-byte unchanged after duplicate execution.
					if len(f.settle(t, child, 132).Changes) != 0 || len(f.settle(t, parent, 132).Changes) != 0 {
						t.Fatal("duplicate payment charged again")
					}
					if got := loadDirect[directPayment](t, f.db, paymentKey); !reflect.DeepEqual(got, p) {
						t.Fatal("duplicate changed child fee state")
					}
					if got := projectMoney(t, f.db); got != baseline {
						t.Fatal("late source changed total assets")
					}
				})
			}
		}
	}
}

// Unlike two outputs of one parent, these obligations can close in different
// transactions. The final compensation must close an already partially fulfilled
// consumer and return its remaining fee budget exactly once.
func TestFeeClosureFulfillmentBeforeCompensation(t *testing.T) {
	for _, ownerFee := range []bool{false, true} {
		t.Run(fmt.Sprintf("owner=%v", ownerFee), func(t *testing.T) {
			f := newDirectFixture(t)
			second := protocol.OutputID(protocol.Digest("second-fee-closure-input"))
			if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				err := state.Put(o, DirectCreationKey(second, 0), state.Creation{Output: f.output, Fact: protocol.Digest("genesis-fact"), Final: true})
				return o.Changes(), err
			}); err != nil {
				t.Fatal(err)
			}
			parents := []DirectPayment{f.payment(t, f.genesis, nil, 1), f.payment(t, second, nil, 2)}
			body := parents[0].Tx.Body
			body.Nonce[0] = 3
			body.Inputs = nil
			body.Outputs = []protocol.Output{f.output}
			body.Outputs[0].Amount = 200
			certs := make([]InputCertificate, 2)
			for i, p := range parents {
				body.Inputs = append(body.Inputs, protocol.Input{Kind: protocol.CertificateInput, Output: p.Certificate.Summary.OutputID(0), Evidence: protocol.Hash(p.Certificate.QC.Fact)})
				certs[i] = InputCertificate{Certificate: p.Certificate}
			}
			sort.Slice(body.Inputs, func(i, j int) bool { return bytes.Compare(body.Inputs[i].Output[:], body.Inputs[j].Output[:]) < 0 })
			base := f.policy.Base.Fee.Register + f.policy.Base.Fee.Settle + f.policy.Base.Fee.Close
			body.Fee.Maximum = base + 2*f.policy.RepairCost
			if ownerFee {
				fee := seedOwnerFee(t, f, "separate-parent-fee", 1500)
				body.Fee = protocol.FeeTerms{Source: protocol.OwnerFinalUTXO, Maximum: body.Fee.Maximum, Inputs: []protocol.Input{fee}, Refund: f.output.Recipient}
				body.Admission = nil
				for _, g := range f.grants {
					if g.Key.Kind != protocol.ResourceFUEL && g.Key.Kind != protocol.ResourcePolicy {
						body.Admission = append(body.Admission, g)
					}
				}
			}
			body.Intent = body.IntentID()
			tx, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: f.output}, {Output: f.output}}, f.policy.Key)
			if err != nil {
				t.Fatal(err)
			}
			tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
			child := f.certify(t, tx, certs)
			money := projectMoney(t, f.db)
			f.settle(t, child, 100)
			f.settle(t, parents[0], 101)
			key := state.Key(keyDirectPayment, child.Certificate.QC.Fact[:])
			p := loadDirect[directPayment](t, f.db, key)
			if p.Pending != 1 || p.Fee.Closed || p.Fee.Validate() != nil {
				t.Fatalf("premature close: %+v", p)
			}
			id := parents[1].Certificate.Summary.OutputID(0)
			for i := 0; i < 2; i++ {
				if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
					tr, err := EvaluateDirectCompensation(v, id, f.policy, 130)
					if i == 1 && len(tr.Changes) != 0 {
						t.Fatal("duplicate compensation changed state")
					}
					return tr.Changes, err
				}); err != nil {
					t.Fatal(err)
				}
			}
			p = loadDirect[directPayment](t, f.db, key)
			if p.Pending != 0 || !p.Fee.Closed || p.Fee.Held != 0 || p.Fee.Validate() != nil || p.Fee.Rewards+p.Fee.Burned != base+f.policy.RepairCost || p.Fee.Refunded != f.policy.RepairCost {
				t.Fatalf("compensation close: %+v", p)
			}
			f.settle(t, parents[1], 131)
			if got := loadDirect[directPayment](t, f.db, key); !reflect.DeepEqual(got, p) {
				t.Fatal("late repayment charged closed consumer")
			}
			if projectMoney(t, f.db) != money {
				t.Fatal("asset projection changed")
			}
		})
	}
}
