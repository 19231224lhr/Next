package rules

import (
	"fmt"
	"testing"

	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestSecurityPartialOutputRepairAndLateSource(t *testing.T) {
	f := newDirectFixture(t)
	base := f.payment(t, f.genesis, nil, 1)
	body := base.Tx.Body
	half := f.output
	half.Amount = 50
	body.Outputs = []protocol.Output{half, half}
	body.Intent = body.IntentID()
	tx, err := protocol.NewFastTx(body, base.Tx.Claims, f.policy.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
	parent := ownerPayment(t, f, f.certify(t, tx, nil), seedOwnerFee(t, f, "split-parent", 1500))
	makeChild := func(index uint32) DirectPayment {
		g := f
		g.output = half
		pay := g.payment(t, parent.Certificate.Summary.OutputID(index), &parent.Certificate, byte(index+2))
		pay.InputCertificates[0].Index = index
		return ownerPayment(t, g, pay, seedOwnerFee(t, f, fmt.Sprint("split-child", index), 1500))
	}
	left, right := makeChild(0), makeChild(1)
	baseline := projectMoney(t, f.db)
	audit := func() {
		t.Helper()
		if got := projectMoney(t, f.db); got != baseline {
			t.Fatalf("got%+v want%+v", got, baseline)
		}
	}
	f.settle(t, left, 100)
	audit()
	pid := parent.Certificate.Summary.OutputID(0)
	if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, e := EvaluateDirectCompensation(v, pid, f.policy, 130)
		return tr.Changes, e
	}); err != nil {
		t.Fatal(err)
	}
	audit()
	coverageKey := state.Key(keyDirectCoverage, parent.Certificate.QC.Fact[:])
	before := loadDirect[directCoverage](t, f.db, coverageKey).Credit
	if before.Paid != 50 || before.Remaining != 50 {
		t.Fatalf("unused promise lost: %+v", before)
	}
	f.settle(t, parent, 131)
	audit()
	after := loadDirect[directCoverage](t, f.db, coverageKey).Credit
	if after.Paid != 50 || after.Discharged != 50 || after.Remaining != 0 {
		t.Fatalf("wrong terminal coverage: %+v", after)
	}
	if !loadDirect[state.Creation](t, f.db, DirectCreationKey(pid, 1)).Final {
		t.Fatal("missing late instance")
	}
	f.settle(t, right, 132)
	audit()
	// Right was unused at source settlement. Consuming it later must use its
	// final creation, not open a new payable obligation for the old certificate.
	err = f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, e := EvaluateDirectCompensation(v, parent.Certificate.Summary.OutputID(1), f.policy, 200)
		if e == nil || len(tr.Changes) != 0 {
			t.Fatal("new compensation after source settled")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := loadDirect[directCoverage](t, f.db, coverageKey).Credit; got != after {
		t.Fatalf("Paid changed after Settled: %+v", got)
	}
	audit()
}

type monetaryProjection struct{ cal, fuel int64 }

// Independently total actual asset locations, not event-reported amounts or
// cumulative Refunded/Rewards fields. All rows are read in one stable View.
func projectMoney(t *testing.T, db store.Store) (p monetaryProjection) {
	t.Helper()
	err := db.View(func(v state.ReadView) error {
		scan := func(kind uint8, visit func(state.Entry)) {
			t.Helper()
			var after []byte
			for {
				rows, err := v.(state.ScanView).Scan(state.Key(kind), after, 1024)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) == 0 {
					return
				}
				for _, row := range rows {
					visit(row)
				}
				after = rows[len(rows)-1].Key
			}
		}
		scan(state.KeyCreation, func(row state.Entry) {
			c, _, err := state.Load[state.Creation](v, row.Key)
			if err != nil {
				t.Fatal(err)
			}
			// Creation and Spend use exactly the same identity/instance suffix.
			key := append([]byte(nil), row.Key...)
			key[2] = state.KeySpend
			s, _, err := state.Load[state.Spend](v, key)
			if err != nil {
				t.Fatal(err)
			}
			if !c.Final || s.Consumed != (protocol.SpendFactID{}) {
				return
			}
			switch c.Output.Asset {
			case protocol.AssetCAL:
				p.cal += int64(c.Output.Amount)
			case protocol.AssetFUEL:
				p.fuel += int64(c.Output.Amount)
			default:
				t.Fatal("unknown asset")
			}
		})
		scan(state.KeyAccount, func(row state.Entry) {
			amount, _, err := state.Load[uint64](v, row.Key)
			if err != nil {
				t.Fatal(err)
			}
			// AccountKey's final field is a one-byte asset discriminator.
			switch protocol.Asset(row.Key[len(row.Key)-1]) {
			case protocol.AssetCAL:
				p.cal += int64(amount)
			case protocol.AssetFUEL:
				p.fuel += int64(amount)
			default:
				t.Fatal("unknown account asset")
			}
		})
		gap := uint64(0)
		scan(keyDirectObligation, func(row state.Entry) {
			ob, _, err := state.Load[DirectObligation](v, row.Key)
			if err != nil {
				t.Fatal(err)
			}
			if ob.Status == DirectOpen {
				gap += ob.Amount
			}
		})
		storedGap, _, err := state.Load[uint64](v, DirectGapKey())
		if err != nil {
			return err
		}
		if storedGap != gap {
			t.Fatalf("gap=%d obligations=%d", storedGap, gap)
		}
		p.cal -= int64(gap)
		scan(keyDirectPayment, func(row state.Entry) {
			payment, _, err := state.Load[directPayment](v, row.Key)
			if err != nil {
				t.Fatal(err)
			}
			f := payment.Fee
			if f.Held+f.Rewards+f.Burned+f.Refunded != f.Maximum {
				t.Fatal("escrow mismatch")
			}
			p.fuel += int64(f.Held)
		})
		for _, kind := range []uint8{state.KeyReward, state.KeyBurned} {
			scan(kind, func(row state.Entry) {
				amount, _, err := state.Load[uint64](v, row.Key)
				if err != nil {
					t.Fatal(err)
				}
				p.fuel += int64(amount)
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSecurityMonetaryProjectionAcrossInterleavings(t *testing.T) {
	// P, C, G are parent/child/grandchild. X and Y repair P/C outputs.
	// T is real external topup. Every successful operation is replayed.
	for _, ownerFee := range []bool{false, true} {
		for _, sequence := range []string{"PCG", "PGC", "CPG", "CGP", "GPC", "GCP", "GTYCXP", "GCXPT", "TGYCXP"} {
			t.Run(fmt.Sprintf("owner%v/%s", ownerFee, sequence), func(t *testing.T) {
				f := newDirectFixture(t)
				makePay := func(input protocol.OutputID, parent *protocol.OutputCertificate, nonce byte) DirectPayment {
					pay := f.payment(t, input, parent, nonce)
					if ownerFee {
						pay = ownerPayment(t, f, pay, seedOwnerFee(t, f, fmt.Sprint(nonce), 1500))
					}
					return pay
				}
				parent := makePay(f.genesis, nil, 1)
				pid := parent.Certificate.Summary.OutputID(0)
				child := makePay(pid, &parent.Certificate, 2)
				cid := child.Certificate.Summary.OutputID(0)
				grand := makePay(cid, &child.Certificate, 3)
				ref := f.grants[0]
				topup := protocol.ReserveIncrease{Network: f.org.Network, Organization: f.org.Hash(), Key: ref.Key, Grant: ref.Grant, Previous: 10000000, Amount: 300}
				topup.Sign(f.owner)
				source := AccountKey(protocol.ReserveFundingAccount(topup.Network, topup.Subject), protocol.AssetCAL)
				if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
					o := state.NewOverlay(v)
					err := state.Put(o, source, uint64(1000))
					return o.Changes(), err
				}); err != nil {
					t.Fatal(err)
				}
				baseline := projectMoney(t, f.db)
				for step, action := range sequence {
					now := int64(100 + step*40)
					for replay := 0; replay < 2; replay++ {
						switch action {
						case 'P':
							f.settle(t, parent, now)
						case 'C':
							f.settle(t, child, now)
						case 'G':
							f.settle(t, grand, now)
						case 'X', 'Y':
							id := pid
							if action == 'Y' {
								id = cid
							}
							if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
								tr, e := EvaluateDirectCompensation(v, id, f.policy, now)
								return tr.Changes, e
							}); err != nil {
								t.Fatal(err)
							}
						case 'T':
							if err := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
								tr, e := EvaluateReserveIncrease(v, topup, map[string]struct{}{string(AccountKey(f.org.Org, protocol.AssetCAL)): {}})
								return tr.Changes, e
							}); err != nil {
								t.Fatal(err)
							}
						}
						if got := projectMoney(t, f.db); got != baseline {
							t.Fatalf("after %c replay%d: got%+v want%+v", action, replay, got, baseline)
						}
					}
				}
			})
		}
	}
}
