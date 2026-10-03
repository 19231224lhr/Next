package member_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

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

type reclaimLab struct {
	f        testkit.Fixture
	p        rules.DirectPolicy
	cfg      [4]member.Config
	m        [4]*member.Member
	db       [4]store.Store
	ledger   store.Store
	engine   *committee.Engine
	path     string
	identity store.Identity
	fee      state.OriginOutput
}

func TestPartialReclaimMultipleInputs(t *testing.T) {
	for _, split := range []bool{false, true} {
		t.Run(map[bool]string{false: "one_winner", true: "two_winners_same_block"}[split], func(t *testing.T) {
			x := newReclaimLab(t, 300)
			base, second := x.tx(t, 0, 1, false), x.tx(t, 1, 2, false)
			body := base.Body
			body.Inputs = append(body.Inputs, second.Body.Inputs...)
			claims := append(base.Claims, second.Claims...)
			if bytes.Compare(body.Inputs[0].Output[:], body.Inputs[1].Output[:]) > 0 {
				body.Inputs[0], body.Inputs[1] = body.Inputs[1], body.Inputs[0]
				claims[0], claims[1] = claims[1], claims[0]
			}
			body.Outputs[0].Amount = 200
			body.Intent = body.IntentID()
			old, err := protocol.NewFastTx(body, claims, x.p.Key)
			if err != nil {
				t.Fatal(err)
			}
			old.Auth = []protocol.OwnerAuth{protocol.SignOwner(old.ID(), x.f.Owner)}
			partial := x.approve(t, old, 0)
			var winners []protocol.DirectPayment
			if split {
				winners = []protocol.DirectPayment{x.approve(t, x.tx(t, 0, 3, false), 1, 2, 3), x.approve(t, x.tx(t, 1, 4, false), 1, 2, 3)}
			} else {
				body.Nonce = x.tx(t, 0, 3, false).Body.Nonce
				body.Intent = body.IntentID()
				win, err := protocol.NewFastTx(body, old.Claims, x.p.Key)
				if err != nil {
					t.Fatal(err)
				}
				win.Auth = []protocol.OwnerAuth{protocol.SignOwner(win.ID(), x.f.Owner)}
				winners = []protocol.DirectPayment{x.approve(t, win, 1, 2, 3)}
			}
			b := x.block(t, winners...)
			if err := blockfollow.Commit(x.db[0], b, x.m[0].PrepareBlock); err != nil {
				t.Fatal(err)
			}
			q, err := x.m[0].Quota(x.f.Genesis.Grants[0].Key, 0)
			if err != nil || q.Available != 200 || q.Reserved != 0 {
				t.Fatal("multiple hits credited incorrectly", q, err)
			}
			out, err := x.m[0].Outcome(partial.Certificate.QC.Fact)
			if err != nil || !out.Invalidated || out.PublicObserved || out.FuelResidual != 0 || out.ExecutionResidual != 0 || out.BytesResidual != 0 {
				t.Fatal(out, err)
			}
		})
	}
}

func TestPartialReclaimRejectsContradictionAtomically(t *testing.T) {
	for _, name := range []string{"observed", "installed", "outbox", "settled", "paid", "pending", "recovered", "fee", "missing_approval", "wrong_input", "same_tx", "underflow", "cross_config", "other_consumed"} {
		t.Run(name, func(t *testing.T) {
			x := newReclaimLab(t)
			old := x.approve(t, x.tx(t, 0, 1, false), 0)
			win := x.approve(t, x.tx(t, 0, 2, false), 1, 2, 3)
			fact := old.Certificate.QC.Fact
			b := x.block(t, win)
			err := x.db[0].Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				p := member.LocalProgress{}
				switch name {
				case "observed":
					o.Set(state.Key(state.KeyObserved, fact[:]), []byte("true"))
				case "installed":
					o.Set(state.Key(state.KeyInstall, fact[:]), []byte("evidence"))
				case "outbox":
					o.Set(state.Key(state.KeyOutbox, fact[:]), []byte("evidence"))
				case "settled":
					p.Settled = true
				case "paid":
					p.Paid = 1
				case "pending":
					p.Pending = 1
				case "recovered":
					p.Recovered = 1
				case "fee":
					p.Fee.Held = 1
				case "missing_approval":
					o.Apply([]state.Change{{Key: state.Key(state.KeyApproval, fact[:]), Delete: true}})
				case "other_consumed":
					if err := state.Put(o, rules.DirectSpendKey(old.Tx.Body.Inputs[0].Output, 0), state.Spend{Candidate: fact, Consumed: protocol.SpendFactID(protocol.Digest("another QC"))}); err != nil {
						return nil, err
					}
				case "wrong_input", "same_tx", "underflow", "cross_config":
					a, _, err := state.Load[state.Approval](o, state.Key(state.KeyApproval, fact[:]))
					if err != nil {
						return nil, err
					}
					if name == "wrong_input" {
						a.Direct.Body.Inputs[0].Output = x.f.Genesis.Outputs[1].ID
					}
					if name == "same_tx" {
						a.Direct = &win.Tx
					}
					if name == "cross_config" {
						a.Direct.Body.Config = protocol.Digest("other config")
					}
					if name == "underflow" {
						d := a.Debits[len(a.Debits)-1]
						if err := state.Put(o, state.SliceKey(d.Key, d.Worker), state.Slice{}); err != nil {
							return nil, err
						}
					}
					if err := state.Put(o, state.Key(state.KeyApproval, fact[:]), a); err != nil {
						return nil, err
					}
				}
				if err := state.Put(o, member.ProgressKey(fact), p); err != nil {
					return nil, err
				}
				return o.Changes(), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			before, err := store.Scan(x.db[0], nil, nil, 1000)
			if err != nil {
				t.Fatal(err)
			}
			if err := blockfollow.Commit(x.db[0], b, x.m[0].PrepareBlock); err == nil {
				t.Fatal("contradiction accepted")
			}
			after, err := store.Scan(x.db[0], nil, nil, 1000)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("failed block leaked changes", err)
			}
		})
	}
}

func TestPartialReclaimRequiresSuccessfulExecution(t *testing.T) {
	x := newReclaimLab(t)
	x.approve(t, x.tx(t, 0, 1, false), 0)
	win := x.approve(t, x.tx(t, 0, 2, false), 1, 2, 3)
	raw, err := win.Submission().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	trust, data := testkit.Block("reclaim", 1, nil, [][]byte{raw}, []*abci.ExecTxResult{{Code: 1}})
	b, err := finality.VerifyBlock(trust, data)
	if err != nil {
		t.Fatal(err)
	}
	if err = blockfollow.Commit(x.db[0], b, x.m[0].PrepareBlock); err != nil {
		t.Fatal(err)
	}
	q, err := x.m[0].Quota(x.f.Genesis.Grants[0].Key, 0)
	if err != nil || q.Reserved != 100 || q.Available != 0 {
		t.Fatal("failed execution released capacity", q, err)
	}
}

func newReclaimLab(t *testing.T, budget ...uint64) *reclaimLab {
	t.Helper()
	x := &reclaimLab{f: testkit.NewFixture("reclaim", "org", 3)}
	x.f.EnableDirect()
	// Each member gets floor(2*150/3)=100 CAL: one approval exhausts it.
	x.f.Genesis.Grants[0].Amount = 150
	if len(budget) != 0 {
		x.f.Genesis.Grants[0].Amount = budget[0]
	}
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(raw)
	k, err := x509.ParsePKCS1PrivateKey(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	settings := rules.DirectSettings{Modulus: k.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	x.p, err = settings.Policy(x.f.Schedule, []protocol.OrgConfig{x.f.Org})
	if err != nil {
		t.Fatal(err)
	}
	x.fee = x.f.Genesis.Outputs[0]
	x.fee.ID = protocol.OutputID(protocol.Digest("reclaim-fee"))
	x.fee.Fact = protocol.Digest("reclaim-fee-final")
	x.fee.Output.Asset, x.fee.Output.Amount = protocol.AssetFUEL, 10000
	x.f.Genesis.Outputs = append(x.f.Genesis.Outputs, x.fee)
	x.path = filepath.Join(t.TempDir(), "member.db")
	x.identity = store.Identity{Network: "reclaim", Role: "member", Node: "0", Schema: member.DirectStoreSchema}
	for i := range x.m {
		x.cfg[i] = member.Config{Organization: x.f.Org, Index: uint16(i), Key: x.f.Keys[i], Peers: []protocol.OrgConfig{x.f.Org}, Schedule: x.f.Schedule, Workers: 1, Direct: &settings}
		if i == 0 {
			x.db[i], err = store.Open(x.path, x.identity)
		} else {
			x.db[i] = store.NewMemory()
		}
		if err != nil {
			t.Fatal(err)
		}
		x.m[i], err = member.New(x.cfg[i], x.db[i], x.f.Genesis)
		if err != nil {
			t.Fatal(err)
		}
	}
	x.ledger = store.NewMemory()
	x.engine, err = committee.NewEngine(committee.EngineConfig{Network: x.f.Org.Network, Organizations: []protocol.OrgConfig{x.f.Org}, Schedule: x.f.Schedule, Genesis: x.f.Genesis, Direct: &settings, Accounts: []committee.GenesisAccount{{Owner: x.f.Org.Org, Asset: protocol.AssetCAL, Balance: 1_000_000_000}, {Owner: x.f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1_000_000_000}}}, x.ledger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, db := range x.db {
			db.Close()
		}
		x.ledger.Close()
	})
	return x
}

func (x *reclaimLab) tx(t *testing.T, index int, nonce uint64, owner bool) protocol.FastTx {
	t.Helper()
	tx, err := x.f.FastTransaction(index, nonce, x.p)
	if err != nil {
		t.Fatal(err)
	}
	if owner {
		body := tx.Body
		body.Fee = protocol.FeeTerms{Source: protocol.OwnerFinalUTXO, Maximum: 1000, Inputs: []protocol.Input{{Kind: protocol.FinalInput, Output: x.fee.ID, Evidence: x.fee.Fact}}, Refund: x.fee.Output.Recipient}
		body.Admission = nil
		for _, g := range x.f.Genesis.Grants {
			if g.Key.Kind != protocol.ResourceFUEL && g.Key.Kind != protocol.ResourcePolicy {
				body.Admission = append(body.Admission, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
			}
		}
		body.Intent = body.IntentID()
		tx, err = protocol.NewFastTx(body, tx.Claims, x.p.Key)
		if err != nil {
			t.Fatal(err)
		}
		tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), x.f.Owner)}
	}
	return tx
}

func (x *reclaimLab) approve(t *testing.T, tx protocol.FastTx, indices ...int) protocol.DirectPayment {
	t.Helper()
	p := protocol.DirectPayment{Tx: tx}
	for _, i := range indices {
		v, err := x.m[i].ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx})
		if err != nil {
			t.Fatal("approve", i, err)
		}
		p.Certificate.Summary = v.Summary
		p.Certificate.QC.Fact = v.Summary.Fact()
		p.Certificate.QC.Votes = append(p.Certificate.QC.Votes, v.Vote)
	}
	return p
}

func (x *reclaimLab) block(t *testing.T, payments ...protocol.DirectPayment) finality.VerifiedBlock {
	t.Helper()
	var wires [][]byte
	var results []*abci.ExecTxResult
	for _, p := range payments {
		if err := p.Certificate.Verify(x.f.Org); err != nil {
			t.Fatal(err)
		}
		raw, err := p.Submission().MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		var tr state.Transition
		err = x.ledger.Update(func(v state.ReadView) ([]state.Change, error) {
			var err error
			tr, err = x.engine.ExecuteAt(v, raw, committee.BlockContext{Height: 1, Time: time.Unix(100, 0)})
			return tr.Changes, err
		})
		if err != nil {
			t.Fatal("execute", err)
		}
		wires = append(wires, raw)
		results = append(results, &abci.ExecTxResult{Data: tr.Data})
	}
	trust, data := testkit.Block("reclaim", 1, nil, wires, results)
	b, err := finality.VerifyBlock(trust, data)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPartialReclaimPublicConflict(t *testing.T) {
	for _, sc := range []struct {
		name                    string
		owner, feeOnly, install bool
	}{{name: "sponsor"}, {name: "owner", owner: true}, {name: "fee_only", owner: true, feeOnly: true}, {name: "installed_winner", install: true}} {
		t.Run(sc.name, func(t *testing.T) {
			x := newReclaimLab(t)
			old := x.approve(t, x.tx(t, 0, 1, sc.owner), 0)
			if _, err := x.m[0].ApproveDirect(context.Background(), protocol.DirectRequest{Tx: x.tx(t, 2, 99, false)}); !errors.Is(err, rules.ErrLimited) {
				t.Fatal("partial approval did not exhaust CAL capacity", err)
			}
			idx := 0
			if sc.feeOnly {
				idx = 1
			}
			win := x.approve(t, x.tx(t, idx, 2, sc.owner), 1, 2, 3)
			fact := old.Certificate.QC.Fact
			var a state.Approval
			var original []byte
			if err := x.db[0].View(func(v state.ReadView) error {
				var err error
				a, _, err = state.Load[state.Approval](v, state.Key(state.KeyApproval, fact[:]))
				if err != nil {
					return err
				}
				original, err = v.Get(state.Key(state.KeyApproval, fact[:]))
				return err
			}); err != nil {
				t.Fatal(err)
			}
			before := make([]state.Slice, len(a.Debits))
			for i, d := range a.Debits {
				q, err := x.m[0].Quota(d.Key, d.Worker)
				if err != nil {
					t.Fatal(err)
				}
				before[i] = q
				if q.Reserved != d.Cap {
					t.Fatal("reservation did not occur")
				}
			}
			if sc.install {
				if err := x.m[0].InstallDirect(win); err != nil {
					t.Fatal(err)
				}
				q, err := x.m[0].Quota(a.Debits[0].Key, a.Debits[0].Worker)
				if err != nil || q != before[0] {
					t.Fatal("INSTALL released partial approval", err)
				}
			}
			b := x.block(t, win)
			for i, m := range x.m {
				if err := blockfollow.Commit(x.db[i], b, m.PrepareBlock); err != nil {
					t.Fatal(err)
				}
			}
			for repeat := 0; repeat < 2; repeat++ {
				if err := blockfollow.Commit(x.db[0], b, x.m[0].PrepareBlock); err != nil {
					t.Fatal(err)
				}
				for i, d := range a.Debits {
					q, err := x.m[0].Quota(d.Key, d.Worker)
					if err != nil {
						t.Fatal(err)
					}
					if q.Reserved != 0 || q.Available != before[i].Available+d.Cap {
						t.Fatalf("old partial approval still occupies resource %v: %+v", d.Key.Kind, q)
					}
				}
				if err := x.db[0].View(func(v state.ReadView) error {
					p, found, err := state.Load[member.LocalProgress](v, member.ProgressKey(fact))
					if err != nil || !found || !p.Invalidated || p.Settled || p.Fee != (rules.Escrow{}) || p.SupersededBy != win.Certificate.QC.Fact || p.InvalidatedHeight != 1 {
						t.Fatal("incorrect terminal state", p, err)
					}
					if sc.feeOnly {
						s, _, err := state.Load[state.Spend](v, rules.DirectSpendKey(old.Tx.Body.Inputs[0].Output, 0))
						if err != nil || s.Candidate != fact || s.Consumed != (protocol.SpendFactID{}) {
							t.Fatal("unrelated input lock was cleared", s, err)
						}
					}
					current, err := v.Get(state.Key(state.KeyApproval, fact[:]))
					if !bytes.Equal(original, current) {
						t.Fatal("approval mutated")
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				if _, err := x.m[0].ApproveDirect(context.Background(), protocol.DirectRequest{Tx: old.Tx}); !errors.Is(err, rules.ErrConflict) {
					t.Fatal("invalidated approval was re-signed", err)
				}
				// Deliberately use all test keys to fabricate the impossible conflicting
				// QC. Even corrupted evidence must not resurrect an invalidated record.
				forged := old
				forged.Certificate.QC.Votes = nil
				for i := 0; i < 3; i++ {
					forged.Certificate.QC.Votes = append(forged.Certificate.QC.Votes, protocol.SignSpend(fact, uint16(i), x.f.Keys[i]))
				}
				if err := x.m[0].InstallDirect(forged); !errors.Is(err, rules.ErrConflict) {
					t.Fatal("late INSTALL resurrected approval", err)
				}
				if repeat == 0 {
					if err := x.db[0].Close(); err != nil {
						t.Fatal(err)
					}
					var err error
					oldIdentity := x.identity
					oldIdentity.Schema = 7
					if oldDB, e := store.Open(x.path, oldIdentity); !errors.Is(e, store.ErrIdentity) {
						if oldDB != nil {
							oldDB.Close()
						}
						t.Fatal("schema7 accepted invalidation state", e)
					}
					x.db[0], err = store.Open(x.path, x.identity)
					if err != nil {
						t.Fatal(err)
					}
					x.m[0], err = member.New(x.cfg[0], x.db[0], x.f.Genesis)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			// Recovered member participates while one of the former signers is absent.
			next := x.tx(t, 2, 3, false)
			body := next.Body
			id := win.Certificate.Summary.OutputID(0)
			body.Inputs = []protocol.Input{{Kind: protocol.FinalInput, Output: id, Evidence: protocol.CreationIdentity(body.Network, win.Tx.ID(), 0, 0)}}
			body.Intent = body.IntentID()
			next, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: win.Tx.Body.Outputs[0]}}, x.p.Key)
			if err != nil {
				t.Fatal(err)
			}
			next.Auth = []protocol.OwnerAuth{protocol.SignOwner(next.ID(), x.f.Owner)}
			if err := x.approve(t, next, 0, 1, 2).Certificate.Verify(x.f.Org); err != nil {
				t.Fatal("capacity did not restore three-vote service", err)
			}
		})
	}
}
