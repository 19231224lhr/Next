package rules

import (
	"bytes"
	"fmt"
	"sort"
	"testing"
	"utxo/internal/state"
	"utxo/protocol"
)

// Split, independently forward, then merge. Exhaust all public arrival orders;
// a merge may execute before both branches and its common ancestor.
func TestSourceRecoverySplitMergeOrders(t *testing.T) {
	var visit func([]int, []int)
	visit = func(order, rest []int) {
		if len(rest) > 0 {
			for i, n := range rest {
				next := append([]int(nil), rest[:i]...)
				next = append(next, rest[i+1:]...)
				visit(append(append([]int(nil), order...), n), next)
			}
			return
		}
		for _, compensate := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/compensate%v", order, compensate), func(t *testing.T) {
				f := newDirectFixture(t)
				orgs := recoveryOrganizations(t, f, []int{0, 1})
				type source struct {
					p     *DirectPayment
					index uint32
				}
				makePay := func(issuer directFixture, nonce byte, parents []source, outputs []protocol.Output) DirectPayment {
					base := issuer.payment(t, f.genesis, nil, nonce)
					body := base.Tx.Body
					var claims []protocol.InputClaim
					var certs []InputCertificate
					if len(parents) == 0 {
						claims = []protocol.InputClaim{{Output: f.output}}
					} else {
						sort.Slice(parents, func(i, j int) bool {
							a := parents[i].p.Certificate.Summary.OutputID(parents[i].index)
							b := parents[j].p.Certificate.Summary.OutputID(parents[j].index)
							return bytes.Compare(a[:], b[:]) < 0
						})
						body.Inputs = nil
						for _, s := range parents {
							body.Inputs = append(body.Inputs, protocol.Input{Kind: protocol.CertificateInput, Output: s.p.Certificate.Summary.OutputID(s.index), Evidence: protocol.Hash(s.p.Certificate.QC.Fact)})
							claims = append(claims, protocol.InputClaim{Output: s.p.Tx.Body.Outputs[s.index]})
							certs = append(certs, InputCertificate{Certificate: s.p.Certificate, Index: s.index})
						}
					}
					body.Outputs = outputs
					body.Intent = body.IntentID()
					tx, e := protocol.NewFastTx(body, claims, f.policy.Key)
					if e != nil {
						t.Fatal(e)
					}
					tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
					return ownerPayment(t, issuer, issuer.certify(t, tx, certs), seedOwnerFee(t, issuer, fmt.Sprintf("dag-fee-%d", nonce), 1500))
				}
				left, right := orgs[1].output, orgs[1].output
				left.Amount = 40
				right.Amount = 60
				p := makePay(orgs[0], 91, nil, []protocol.Output{left, right})
				left.Recipient = orgs[0].output.Recipient
				right.Recipient = orgs[0].output.Recipient
				a := makePay(orgs[1], 92, []source{{&p, 0}}, []protocol.Output{left})
				b := makePay(orgs[1], 93, []source{{&p, 1}}, []protocol.Output{right})
				merge := makePay(orgs[0], 94, []source{{&a, 0}, {&b, 0}}, []protocol.Output{orgs[0].output})
				payments := []DirectPayment{p, a, b, merge}
				initial := projectMoney(t, f.db)
				audit := func() {
					t.Helper()
					if got := projectMoney(t, f.db); got != initial {
						t.Fatalf("projection changed: %+v / %+v", got, initial)
					}
				}
				for step, index := range order {
					now := int64(100 + 40*step)
					if compensate {
						for _, pay := range payments {
							for i := range pay.Tx.Body.Outputs {
								id := pay.Certificate.Summary.OutputID(uint32(i))
								if e := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
									ob, found, e := state.Load[DirectObligation](v, DirectObligationKey(id))
									if e != nil || !found || ob.Status != DirectOpen {
										return nil, e
									}
									tr, e := EvaluateDirectCompensation(v, id, f.policy, now)
									return tr.Changes, e
								}); e != nil {
									t.Fatal(e)
								}
								audit()
							}
						}
					}
					f.settle(t, payments[index], now)
					audit()
				}
				for _, issuer := range orgs {
					if got := loadDirect[uint64](t, f.db, AccountKey(issuer.org.Org, protocol.AssetCAL)); got != 10000000 {
						t.Fatal("issuer reserve not restored", got)
					}
					if e := f.db.View(func(v state.ReadView) error {
						u, _, e := state.Load[PublicUsage](v, state.Key(state.KeyUsage, issuer.grants[0].Key.Encode()))
						if u.Spent != 0 || u.Reserved != 0 {
							t.Fatal("unclosed usage", u)
						}
						return e
					}); e != nil {
						t.Fatal(e)
					}
				}
				for _, pay := range payments[:3] {
					for i := range pay.Tx.Body.Outputs {
						id := pay.Certificate.Summary.OutputID(uint32(i))
						if s := loadDirect[state.Spend](t, f.db, DirectSpendKey(id, 0)); s.Consumed == (protocol.SpendFactID{}) {
							t.Fatal("source output revived")
						}
					}
				}
				end := loadDirect[state.Creation](t, f.db, DirectCreationKey(merge.Certificate.Summary.OutputID(0), 0))
				if !end.Final || end.Output != orgs[0].output {
					t.Fatal("merged principal changed")
				}
			})
		}
	}
	visit(nil, []int{0, 1, 2, 3})
}
