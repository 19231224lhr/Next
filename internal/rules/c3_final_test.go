package rules

import (
	"math"
	"reflect"
	"testing"
	"utxo/internal/state"
	"utxo/protocol"
)

func finalFixture(t *testing.T) directFixture {
	f := c3Fixture(t)
	f.policy.C3.Policies[0].Version = 2
	f.policy.C3.Committee = []C3Validator{{ID: protocol.Digest("validator"), Weight: 1, Address: []byte{1}}}
	f.policy.Includer = f.policy.C3.Committee[0].ID
	f.policy.C3.EpochBlocks = 2
	f.policy.C3.ReservedBPS = 2500
	f.policy.C3.Policies[0].Schedule = protocol.FeeSchedule{AttachReward: 1, ExecuteReward: 2, ResolveReward: 3, Compensate: 4, Repay: 5}
	return f
}
func finalPayment(t *testing.T, f directFixture, parent *protocol.OutputCertificate, n byte) DirectPayment {
	if parent != nil {
		f.output.Amount = parent.Summary.Outputs[0].Amount
	}
	legacy := f
	legacy.policy.C3 = nil
	legacy.grants = legacy.grants[:len(legacy.grants)-1]
	in := f.genesis
	if parent != nil {
		in = parent.Summary.OutputID(0)
	}
	p := legacy.payment(t, in, parent, n)
	b := p.Tx.Body
	b.Fee = protocol.FeeTerms{Source: protocol.CALService}
	if e := BindC3(&b, f.policy, f.grants); e != nil {
		t.Fatal(e)
	}
	if b.Fee.Maximum >= b.Outputs[0].Amount {
		t.Fatal("quote consumes output")
	}
	b.Outputs[0].Amount -= b.Fee.Maximum
	tx, e := protocol.NewFastTx(b, p.Tx.Claims, f.policy.Key)
	if e != nil {
		t.Fatal(e)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
	return f.certify(t, tx, p.InputCertificates)
}
func TestFinalC3OrderAndTimeout(t *testing.T) {
	for _, mode := range []string{"ordered", "reverse", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			f := finalFixture(t)
			source := finalPayment(t, f, nil, 1)
			child := finalPayment(t, f, &source.Certificate, 2)
			if mode == "ordered" {
				f.settle(t, source, 100)
				f.settle(t, child, 101)
			} else {
				f.settle(t, child, 100)
				if mode == "timeout" {
					if e := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
						tr, e := EvaluateDirectCompensation(v, source.Certificate.Summary.OutputID(0), f.policy, 131)
						return tr.Changes, e
					}); e != nil {
						t.Fatal(e)
					}
					r := loadDirect[C3Record](t, f.db, C3EscrowKey(source.Certificate.Summary.Fact()))
					if r.Escrow.Held != 7 {
						t.Fatalf("source reward/repay hold %d", r.Escrow.Held)
					}
				}
				f.settle(t, source, 132)
			}
			r := loadDirect[C3Record](t, f.db, C3EscrowKey(source.Certificate.Summary.Fact()))
			if r.Escrow.Rewards != 6 || r.Escrow.Held != 0 {
				t.Fatalf("fixed reward %+v", r)
			}
			if r.Escrow.Burned != 0 {
				t.Fatal("final fuel burned")
			}
			wantReflow := uint64(0)
			if mode == "timeout" {
				wantReflow = 9
			}
			if r.Escrow.Reflow != wantReflow {
				t.Fatalf("timeout reflow %d", r.Escrow.Reflow)
			}
			before := loadDirect[C3PoolTotals](t, f.db, C3PoolTotalsKey(source.Tx.Body.Fee.C3.Funding().Key))
			f.settle(t, source, 133)
			after := loadDirect[C3PoolTotals](t, f.db, C3PoolTotalsKey(source.Tx.Body.Fee.C3.Funding().Key))
			if before != after {
				t.Fatal("repeat spent fees twice")
			}
		})
	}
}

func TestFinalC3EpochPayerExclusionAndDust(t *testing.T) {
	f := finalFixture(t)
	for i := 1; i < 3; i++ {
		cfg := f.org
		cfg.Org = protocol.Digest("other-issuer", []byte{byte(i)})
		f.policy.Organizations[cfg.Hash()] = cfg
		funding := protocol.AdmissionRef{Key: protocol.ResourceKey{Kind: protocol.ResourceFUEL, Account: protocol.Digest("other-pool", []byte{byte(i)}), Version: 1}, Grant: protocol.Digest("other-grant", []byte{byte(i)})}
		f.policy.C3.Issuers = append(f.policy.C3.Issuers, C3Issuer{Config: cfg.Hash(), Funding: funding})
	}
	update := func(fn func(*state.Overlay) error) {
		t.Helper()
		if e := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
			o := state.NewOverlay(v)
			e := fn(o)
			return o.Changes(), e
		}); e != nil {
			t.Fatal(e)
		}
	}
	update(func(o *state.Overlay) error {
		for i, issuer := range f.policy.C3.Issuers {
			if e := state.Put(o, AccountKey(f.policy.Organizations[issuer.Config].Org, protocol.AssetCAL), uint64((i+1)*10)); e != nil {
				return e
			}
			if i > 0 {
				if e := state.Put(o, state.Key(state.KeyGrant, issuer.Funding.Key.Encode()), state.Grant{ID: issuer.Funding.Grant, Key: issuer.Funding.Key, Organization: issuer.Config, Amount: 100}); e != nil {
					return e
				}
			}
		}
		return nil
	})
	begin := func(h uint64) {
		t.Helper()
		if e := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
			tr, e := BeginC3Epoch(v, f.policy, h)
			return tr.Changes, e
		}); e != nil {
			t.Fatal(e)
		}
	}
	begin(1)
	update(func(o *state.Overlay) error { return addC3Pool(o, f.org.Org, 11) })
	begin(2)
	begin(3)
	s := loadDirect[C3EpochState](t, f.db, C3EpochKey())
	if len(s.Pool) != 1 || s.Pool[0].Payer != f.org.Org || s.Pool[0].Amount != 1 {
		t.Fatalf("lost payer dust %+v", s)
	}
	got := map[protocol.Hash]uint64{}
	for _, c := range s.Credits {
		got[c.Organization] = c.Cumulative
	}
	if got[f.org.Hash()] != 0 || got[f.policy.C3.Issuers[1].Config] != 4 || got[f.policy.C3.Issuers[2].Config] != 6 {
		t.Fatalf("payer exclusion %+v", got)
	}
	before := s
	begin(3)
	after := loadDirect[C3EpochState](t, f.db, C3EpochKey())
	if after.LastHeight != before.LastHeight || after.Pool[0] != before.Pool[0] {
		t.Fatal("epoch replay")
	}
	begin(4)
	begin(5)
	s = loadDirect[C3EpochState](t, f.db, C3EpochKey())
	if s.Pool[0].Amount != 1 || s.Pool[0].Payer != f.org.Org {
		t.Fatal("carry attribution lost")
	}
}

func TestFinalC3SolePayerRetainsFees(t *testing.T) {
	f := finalFixture(t)
	for h := uint64(1); h <= 3; h++ {
		if e := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
			tr, e := BeginC3Epoch(v, f.policy, h)
			o := state.NewOverlay(v)
			o.Apply(tr.Changes)
			if e == nil && h == 1 {
				e = addC3Pool(o, f.org.Org, 9)
			}
			return o.Changes(), e
		}); e != nil {
			t.Fatal(e)
		}
	}
	s := loadDirect[C3EpochState](t, f.db, C3EpochKey())
	if len(s.Credits) != 0 || s.Pool[0].Amount != 9 {
		t.Fatal("sole payer received own fees")
	}
}

func TestFinalC3CALFeeIsAtomicAndSigned(t *testing.T) {
	f := finalFixture(t)
	f.policy.C3.Policies[0].BaseFee = 2
	source := finalPayment(t, f, nil, 1)
	child := finalPayment(t, f, &source.Certificate, 2)
	if source.Tx.Body.Outputs[0].Amount != 98 || child.Tx.Body.Outputs[0].Amount != 96 {
		t.Fatal("recipient amount/fee arithmetic")
	}
	if len(source.Tx.Body.Fee.Inputs) != 0 || source.Tx.Body.Fee.Source != protocol.CALService {
		t.Fatal("user fuel required")
	}
	f.settle(t, child, 100)
	revenue := source.Tx.Body.Fee.C3.Revenue
	if got := loadDirect[uint64](t, f.db, AccountKey(revenue, protocol.AssetCAL)); got != 2 {
		t.Fatalf("unexecuted source collected CAL %d", got)
	}
	modified := source.Tx
	modified.Body.Fee.C3.BaseFee++
	modified.Body.Fee.C3.Quote++
	modified.Body.Fee.Maximum++
	if modified.VerifyAuth() == nil {
		t.Fatal("CAL fee mutation kept owner signature valid")
	}
	f.settle(t, source, 101)
	f.settle(t, source, 102)
	if got := loadDirect[uint64](t, f.db, AccountKey(revenue, protocol.AssetCAL)); got != 4 {
		t.Fatalf("CAL fee duplicate %d", got)
	}
}

func TestFinalC3MemberCreditOnlyAddsObservedDelta(t *testing.T) {
	f := finalFixture(t)
	issuer := f.policy.C3.Issuers[0]
	key := issuer.Funding.Key
	if e := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if e := state.Put(o, state.SliceKey(key, 0), state.Slice{Available: 600, Reserved: 66, Generation: 1}); e != nil {
			return nil, e
		}
		credit := protocol.FuelCredit{Key: key, Grant: issuer.Funding.Grant, Organization: f.org.Hash(), Cumulative: 9}
		if e := ApplyC3Credit(o, credit, f.org.Hash(), 1); e != nil {
			return nil, e
		}
		if e := ApplyC3Credit(o, credit, f.org.Hash(), 1); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	}); e != nil {
		t.Fatal(e)
	}
	got := loadDirect[state.Slice](t, f.db, state.SliceKey(key, 0))
	if got.Available != 606 || got.Reserved != 66 {
		t.Fatalf("credit changed spent/held liabilities %+v", got)
	}
}

func TestFinalC3MultiOutputQuorumDedupAndLateSource(t *testing.T) {
	f := finalFixture(t)
	source := finalPayment(t, f, nil, 1)
	body := source.Tx.Body
	body.Outputs = append(body.Outputs, body.Outputs[0])
	body.Outputs[0].Amount = 40
	body.Outputs[1].Amount = 60
	if e := BindC3(&body, f.policy, f.grants); e != nil {
		t.Fatal(e)
	}
	tx, e := protocol.NewFastTx(body, source.Tx.Claims, f.policy.Key)
	if e != nil {
		t.Fatal(e)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
	source = f.certify(t, tx, nil)
	makeChild := func(index uint32) DirectPayment {
		ff := f
		ff.output.Amount = body.Outputs[index].Amount
		// Build a normal body, then bind the selected direct source output.
		p := finalPayment(t, ff, nil, byte(index+2))
		b := p.Tx.Body
		b.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: source.Certificate.Summary.OutputID(index), Evidence: protocol.Hash(source.Certificate.QC.Fact)}}
		tx, e := protocol.NewFastTx(b, p.Tx.Claims, f.policy.Key)
		if e != nil {
			t.Fatal(e)
		}
		tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
		parent := source.Certificate
		parent.QC.Votes = nil
		for _, i := range []uint16{0, 2, 3} {
			parent.QC.Votes = append(parent.QC.Votes, protocol.SignSpend(parent.QC.Fact, i, f.members[i]))
		}
		return f.certify(t, tx, []InputCertificate{{Certificate: parent, Index: index}})
	}
	first, second := makeChild(0), makeChild(1)
	f.settle(t, first, 100)
	f.settle(t, second, 101)
	r := loadDirect[C3Record](t, f.db, C3EscrowKey(source.Certificate.QC.Fact))
	if r.Escrow.Rewards != 1 || r.Escrow.Held != 26 {
		t.Fatalf("duplicate quorum attachment %+v", r)
	}
	if e = f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, e := EvaluateDirectCompensation(v, source.Certificate.Summary.OutputID(0), f.policy, 131)
		return tr.Changes, e
	}); e != nil {
		t.Fatal(e)
	}
	r = loadDirect[C3Record](t, f.db, C3EscrowKey(source.Certificate.QC.Fact))
	if r.Escrow.Held != 19 {
		t.Fatalf("multi-output future cost %d", r.Escrow.Held)
	}
	f.settle(t, source, 132)
	r = loadDirect[C3Record](t, f.db, C3EscrowKey(source.Certificate.QC.Fact))
	if r.Escrow.Rewards != 9 || r.Escrow.Reflow != 9 || r.Escrow.Released != 9 || r.Escrow.Held != 0 {
		t.Fatalf("multi-output closure %+v", r)
	}
}

func TestFinalC3FailureLeavesNoEscrowOrFee(t *testing.T) {
	f := finalFixture(t)
	p := finalPayment(t, f, nil, 1)
	b := p.Tx.Body
	b.Outputs[0].Amount++
	tx, e := protocol.NewFastTx(b, p.Tx.Claims, f.policy.Key)
	if e != nil {
		t.Fatal(e)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
	bad := f.certify(t, tx, nil)
	verified, e := VerifyDirectPayment(bad, f.policy)
	if e != nil {
		t.Fatal(e)
	}
	before := loadDirect[uint64](t, f.db, AccountKey(b.Fee.C3.Pool, protocol.AssetFUEL))
	if e = f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, e := EvaluateDirectPayment(v, verified, f.policy, 100)
		return tr.Changes, e
	}); e == nil {
		t.Fatal("unbalanced CAL executed")
	}
	if got := loadDirect[uint64](t, f.db, AccountKey(b.Fee.C3.Pool, protocol.AssetFUEL)); got != before {
		t.Fatal("failed transition debited pool")
	}
	if e = f.db.View(func(v state.ReadView) error {
		_, found, e := state.Load[C3Record](v, C3EscrowKey(bad.Certificate.Summary.Fact()))
		if found {
			t.Fatal("failed transition published escrow")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}

func TestFinalC3EpochWeightsIgnoreLastBlockTopup(t *testing.T) {
	f := finalFixture(t)
	for h := uint64(1); h <= 3; h++ {
		if e := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
			tr, e := BeginC3Epoch(v, f.policy, h)
			o := state.NewOverlay(v)
			o.Apply(tr.Changes)
			if e == nil && h == 2 {
				e = state.Put(o, AccountKey(f.org.Org, protocol.AssetCAL), uint64(1<<40))
			}
			return o.Changes(), e
		}); e != nil {
			t.Fatal(e)
		}
	}
	old := loadDirect[C3EpochState](t, f.db, C3EpochHistoryKey(0))
	current := loadDirect[C3EpochState](t, f.db, C3EpochKey())
	if reflect.DeepEqual(old.Weights, current.Weights) || old.Weights[0] >= 1<<40 || current.Weights[0] != 1<<40 {
		t.Fatalf("end epoch topup weight old=%v current=%v", old.Weights, current.Weights)
	}
}

func TestFinalC3WeightedRewardsConserveAtUint64Limit(t *testing.T) {
	f := finalFixture(t)
	validators := []C3Validator{{ID: protocol.Digest("v0"), Weight: 1}, {ID: protocol.Digest("v1"), Weight: 2}, {ID: protocol.Digest("v2"), Weight: 3}}
	f.policy.C3.Committee = validators
	f.policy.Includer = validators[0].ID
	if e := f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		ev := newDirectEval(v, f.policy, f.org.Network)
		err := ev.c3Reward(math.MaxUint64, 0)
		return ev.o.Changes(), err
	}); e != nil {
		t.Fatal(e)
	}
	var sum uint64
	for _, validator := range validators {
		amount := loadDirect[uint64](t, f.db, state.Key(state.KeyReward, validator.ID[:]))
		var e error
		sum, e = protocol.Add(sum, amount)
		if e != nil {
			t.Fatal(e)
		}
	}
	if sum != math.MaxUint64 {
		t.Fatal("weighted rewards lost integer units")
	}
}

func TestFinalC3PriorityHeldThroughCompensation(t *testing.T) {
	f := finalFixture(t)
	tier := C3Tier{ID: protocol.Digest("priority"), CAL: 3, MinimumReward: 2}
	f.policy.C3.Policies[0].Tiers = []C3Tier{tier}
	source := finalPayment(t, f, nil, 1)
	b := source.Tx.Body
	b.Fee.C3.Tier = tier.ID
	if e := BindC3(&b, f.policy, f.grants); e != nil {
		t.Fatal(e)
	}
	b.Outputs = append([]protocol.Output(nil), b.Outputs...)
	b.Outputs[0].Amount -= 3
	tx, e := protocol.NewFastTx(b, source.Tx.Claims, f.policy.Key)
	if e != nil {
		t.Fatal(e)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.owner)}
	source = f.certify(t, tx, nil)
	child := finalPayment(t, f, &source.Certificate, 2)
	f.settle(t, child, 100)
	if e = f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, e := EvaluateDirectCompensation(v, source.Certificate.Summary.OutputID(0), f.policy, 131)
		return tr.Changes, e
	}); e != nil {
		t.Fatal(e)
	}
	rec := loadDirect[C3Record](t, f.db, C3EscrowKey(source.Certificate.Summary.Fact()))
	if rec.Escrow.Held != 9 || rec.QuoteCollected {
		t.Fatal("priority/source execution released before late source")
	}
	f.settle(t, source, 132)
	rec = loadDirect[C3Record](t, f.db, C3EscrowKey(source.Certificate.Summary.Fact()))
	if rec.Escrow.Rewards != 8 || rec.Escrow.Reflow != 9 || rec.Escrow.Held != 0 || rec.Escrow.Released != 0 {
		t.Fatalf("late priority fee %+v", rec)
	}
}
