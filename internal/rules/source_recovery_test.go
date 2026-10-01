package rules

import (
	"fmt"
	"testing"

	"utxo/internal/state"
	"utxo/protocol"
)

// A delayed self-payment cannot turn the issuer's temporary advance into a
// second user-owned principal when its original source finally executes.
func TestLateSourceRepaysReserveWithoutSecondUserOutput(t *testing.T) {
	f := newDirectFixture(t)
	parent := f.payment(t, f.genesis, nil, 71)
	id := parent.Certificate.Summary.OutputID(0)
	child := f.payment(t, id, &parent.Certificate, 72)
	f.settle(t, child, 100)
	if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, err := EvaluateDirectCompensation(v, id, f.policy, 130)
		return tr.Changes, err
	}); err != nil {
		t.Fatal(err)
	}
	f.settle(t, parent, 131)
	if balance := loadDirect[uint64](t, f.db, AccountKey(f.org.Org, protocol.AssetCAL)); balance != 10000000 {
		t.Errorf("late source did not repay actual reserve payer: got %d want 10000000", balance)
	}
	if err := f.db.View(func(v state.ReadView) error {
		for _, instance := range []uint8{0, 1} {
			c, found, err := state.Load[state.Creation](v, DirectCreationKey(id, instance))
			if err != nil {
				return err
			}
			s, _, err := state.Load[state.Spend](v, DirectSpendKey(id, instance))
			if err != nil {
				return err
			}
			if found && c.Final && s.Consumed == (protocol.SpendFactID{}) {
				t.Errorf("late source credited user again: instance=%d amount=%d", instance, c.Output.Amount)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(f.settle(t, parent, 132).Changes) != 0 {
		t.Fatal("duplicate source was applied twice")
	}
}

// Enumerate all arrival orders, including sources arriving after intermediate
// advances. Every prefix is audited independently from event counters; every
// complete trace must have the same CAL allocation as ordered execution.
func TestSourceRecoveryAllFourHopOrders(t *testing.T) {
	var visit func([]int, []int)
	visit = func(prefix, remaining []int) {
		if len(remaining) > 0 {
			for i, n := range remaining {
				rest := append([]int(nil), remaining[:i]...)
				rest = append(rest, remaining[i+1:]...)
				visit(append(append([]int(nil), prefix...), n), rest)
			}
			return
		}
		for _, routes := range [][]int{{0, 0, 0, 0}, {0, 1, 0, 1}, {0, 1, 2, 0}} {
			for _, compensate := range []bool{false, true} {
				t.Run(fmt.Sprintf("%v/routes%v/compensate%v", prefix, routes, compensate), func(t *testing.T) {
					f := newDirectFixture(t)
					orgs := recoveryOrganizations(t, f, routes)
					payments := make([]DirectPayment, 4)
					for i := range payments {
						issuer := orgs[routes[i]]
						var previous *protocol.OutputCertificate
						id := f.genesis
						if i > 0 {
							previous = &payments[i-1].Certificate
							id = previous.Summary.OutputID(0)
						}
						base := issuer.payment(t, id, previous, byte(80+i))
						body := base.Tx.Body
						if i+1 < len(payments) {
							body.Outputs[0].Recipient = orgs[routes[i+1]].output.Recipient
						}
						body.Intent = body.IntentID()
						tx, err := protocol.NewFastTx(body, base.Tx.Claims, f.policy.Key)
						if err != nil {
							t.Fatal(err)
						}
						tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
						payments[i] = ownerPayment(t, issuer, issuer.certify(t, tx, base.InputCertificates), seedOwnerFee(t, issuer, fmt.Sprint("route-fee-", i), 1500))
					}
					baseline := projectMoney(t, f.db)
					audit := func() {
						t.Helper()
						if got := projectMoney(t, f.db); got != baseline {
							t.Fatalf("asset locations changed: got %+v want %+v", got, baseline)
						}
					}
					for step, index := range prefix {
						now := int64(100 + step*40)
						if compensate {
							for _, payment := range payments {
								id := payment.Certificate.Summary.OutputID(0)
								if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
									ob, found, err := state.Load[DirectObligation](v, DirectObligationKey(id))
									if err != nil || !found || ob.Status != DirectOpen {
										return nil, err
									}
									tr, err := EvaluateDirectCompensation(v, id, f.policy, now)
									return tr.Changes, err
								}); err != nil {
									t.Fatal(err)
								}
								audit()
							}
						}
						f.settle(t, payments[index], now)
						audit()
						if len(f.settle(t, payments[index], now+1).Changes) != 0 {
							t.Fatal("repeated source changed state")
						}
					}
					for _, issuer := range orgs {
						if got := loadDirect[uint64](t, f.db, AccountKey(issuer.org.Org, protocol.AssetCAL)); got != 10000000 {
							t.Fatalf("arrival order caused issuer loss: %d", got)
						}
						if err := f.db.View(func(v state.ReadView) error {
							u, _, err := state.Load[PublicUsage](v, state.Key(state.KeyUsage, issuer.grants[0].Key.Encode()))
							if u.Reserved != 0 || u.Spent != 0 {
								t.Fatalf("unclosed usage: %+v", u)
							}
							return err
						}); err != nil {
							t.Fatal(err)
						}
					}
					for _, pay := range payments {
						if err := f.db.View(func(v state.ReadView) error {
							c, found, err := state.Load[directCoverage](v, state.Key(keyDirectCoverage, pay.Certificate.QC.Fact[:]))
							if found && (c.Credit.Remaining != 0 || c.Credit.Paid != c.Credit.Recovered) {
								t.Fatalf("unclosed coverage: %+v", c.Credit)
							}
							return err
						}); err != nil {
							t.Fatal(err)
						}
					}
					last := payments[3].Certificate.Summary.OutputID(0)
					if got := loadDirect[state.Creation](t, f.db, DirectCreationKey(last, 0)); !got.Final || got.Output.Amount != 100 {
						t.Fatal("terminal recipient principal changed")
					}
					for i := 0; i < 3; i++ {
						id := payments[i].Certificate.Summary.OutputID(0)
						if got := loadDirect[state.Spend](t, f.db, DirectSpendKey(id, 0)); got.Consumed != payments[i+1].Certificate.QC.Fact {
							t.Fatal("source arrival revived an already consumed input")
						}
					}
				})
			}
		}
	}
	visit(nil, []int{0, 1, 2, 3})
}

// Each organization has independent members, reserve and admission grants.
// The same owner forwards between explicitly routed outputs.
func recoveryOrganizations(t *testing.T, first directFixture, routes []int) []directFixture {
	t.Helper()
	count := 1
	for _, i := range routes {
		if i >= count {
			count = i + 1
		}
	}
	orgs := []directFixture{first}
	for i := 1; i < count; i++ {
		g := newDirectFixture(t)
		g.db, g.owner = first.db, first.owner
		g.org.Org = protocol.Digest("recovery-org", []byte{byte(i)})
		g.output.Recipient = protocol.NewDescriptor(g.org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: g.org.Org}, g.owner)
		first.policy.Organizations[g.org.Hash()] = g.org
		g.policy = first.policy
		for n := range g.grants {
			r := &g.grants[n]
			if r.Key.Kind != protocol.ResourcePolicy {
				r.Key.Account = g.org.Org
			}
			r.Grant = protocol.Digest("recovery-grant", g.org.Org[:], r.Key.Encode())
		}
		// Fee policies must be distinct although payment() has a fixed test policy.
		// These tests use owner-paid FUEL instead, constructed below.
		if err := first.db.Update(func(v state.ReadView) ([]state.Change, error) {
			o := state.NewOverlay(v)
			for _, r := range g.grants {
				if r.Key.Kind == protocol.ResourcePolicy {
					continue
				}
				if err := state.Put(o, state.Key(state.KeyGrant, r.Key.Encode()), state.Grant{ID: r.Grant, Organization: g.org.Hash(), Key: r.Key, Amount: 10000000, Subject: g.output.Recipient.Owner}); err != nil {
					return nil, err
				}
			}
			for _, a := range []protocol.Asset{protocol.AssetCAL, protocol.AssetFUEL} {
				if err := state.Put(o, AccountKey(g.org.Org, a), uint64(10000000)); err != nil {
					return nil, err
				}
			}
			return o.Changes(), nil
		}); err != nil {
			t.Fatal(err)
		}
		orgs = append(orgs, g)
	}
	return orgs
}
