package member_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
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

// All deadline jumps below use authenticated block time; the OS clock is untouched.
func boundaryLab(t *testing.T, budget uint64) (*reclaimLab, int64, int64) {
	t.Helper()
	x := newReclaimLab(t, budget)
	for i := range x.m {
		x.cfg[i].DisableSourceRecovery = true
		var err error
		x.m[i], err = member.New(x.cfg[i], x.db[i], x.f.Genesis)
		if err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now().Unix() - 60
	return x, start, start + protocol.ApprovalLifetimeSeconds
}

func boundaryBlock(t *testing.T, x *reclaimLab, height int64, previous []byte, at int64, payments ...protocol.DirectPayment) finality.VerifiedBlock {
	t.Helper()
	var wires [][]byte
	var results []*abci.ExecTxResult
	for _, p := range payments {
		raw, err := p.Submission().MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		var tr state.Transition
		err = x.ledger.Update(func(v state.ReadView) ([]state.Change, error) {
			var err error
			tr, err = x.engine.ExecuteAt(v, raw, committee.BlockContext{Height: height, Time: time.Unix(at, 0)})
			return tr.Changes, err
		})
		if err != nil {
			t.Fatal("committee execution", err)
		}
		wires = append(wires, raw)
		results = append(results, &abci.ExecTxResult{Data: tr.Data})
	}
	trust, raw := testkit.Block("reclaim", height, previous, wires, results, time.Unix(at, 0))
	b, err := finality.VerifyBlock(trust, raw)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func followBoundary(t *testing.T, x *reclaimLab, b finality.VerifiedBlock, indices ...int) {
	t.Helper()
	for _, i := range indices {
		if err := blockfollow.Commit(x.db[i], b, x.m[i].PrepareBlock); err != nil {
			t.Fatal(i, err)
		}
	}
}

func boundaryRows(t *testing.T, db store.Store) any {
	t.Helper()
	rows, err := store.Scan(db, nil, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func boundarySnapshot(t *testing.T, x *reclaimLab, p protocol.DirectPayment, phase string, at int64) {
	t.Helper()
	rec := map[string]any{"case_id": t.Name(), "phase": phase, "block_time": at, "deadline": p.Tx.Body.ApprovalDeadline, "transaction": fmt.Sprintf("%x", p.Tx.ID()), "certificate": fmt.Sprintf("%x", p.Certificate.QC.Fact)}
	var gms []any
	for i, m := range x.m {
		var quotas []any
		for _, g := range x.f.Genesis.Grants {
			q, err := m.Quota(g.Key, 0)
			if err != nil {
				t.Fatal(err)
			}
			quotas = append(quotas, map[string]any{"kind": g.Key.Kind, "slice": q})
		}
		var progress member.LocalProgress
		var spend state.Spend
		var cursor blockfollow.Cursor
		if err := x.db[i].View(func(v state.ReadView) error {
			var err error
			progress, _, err = state.Load[member.LocalProgress](v, member.ProgressKey(p.Certificate.QC.Fact))
			if err != nil {
				return err
			}
			spend, _, err = state.Load[state.Spend](v, rules.DirectSpendKey(p.Tx.Body.Inputs[0].Output, 0))
			if err != nil {
				return err
			}
			cursor, _, err = state.Load[blockfollow.Cursor](v, state.Key(120))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		gms = append(gms, map[string]any{"gm": i, "quotas": quotas, "progress": progress, "input_lock": spend, "confirmed_cursor": cursor})
	}
	rec["gms"] = gms
	if err := x.ledger.View(func(v state.ReadView) error {
		balances := map[string]uint64{}
		for label, asset := range map[string]protocol.Asset{"CAL": protocol.AssetCAL, "FUEL": protocol.AssetFUEL} {
			balance, _, err := state.Load[uint64](v, rules.AccountKey(x.f.Org.Org, asset))
			if err != nil {
				return err
			}
			balances[label] = balance
		}
		rec["public_accounts"] = balances
		_, registeredErr := v.Get(state.Key(100, p.Certificate.QC.Fact[:]))
		rec["registered"] = registeredErr == nil
		_, executedErr := v.Get(state.Key(103, p.Certificate.QC.Fact[:]))
		rec["executed"] = executedErr == nil
		var obligations []rules.DirectObligation
		for i := range p.Tx.Body.Outputs {
			ob, found, err := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(p.Certificate.Summary.OutputID(uint32(i))))
			if err != nil {
				return err
			}
			if found {
				obligations = append(obligations, ob)
			}
		}
		rec["obligations"] = obligations
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(p.InputCertificates) > 0 {
		var deadlines []int64
		for _, source := range p.InputCertificates {
			deadlines = append(deadlines, source.Certificate.Summary.ApprovalDeadline)
		}
		rec["source_deadlines"] = deadlines
	}
	raw, err := json.Marshal(boundaryRows(t, x.ledger))
	if err != nil {
		t.Fatal(err)
	}
	rec["ledger_sha256"] = fmt.Sprintf("%x", sha256.Sum256(raw))
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("EXPIRY_CASE " + string(data))
}

func boundaryChild(t *testing.T, x *reclaimLab, parent protocol.DirectPayment, deadline int64, nonce uint64) protocol.DirectPayment {
	t.Helper()
	body := x.tx(t, 1, nonce, false).Body
	body.ApprovalDeadline = deadline
	body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: parent.Certificate.Summary.OutputID(0), Evidence: protocol.Hash(parent.Certificate.QC.Fact)}}
	body.Outputs = []protocol.Output{parent.Tx.Body.Outputs[0]}
	tx, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: parent.Tx.Body.Outputs[0]}}, x.p.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), x.f.Owner)}
	proofs := []protocol.InputCertificate{{Certificate: parent.Certificate, Index: 0}}
	p := protocol.DirectPayment{Tx: tx, InputCertificates: proofs}
	for _, i := range []int{1, 2, 3} {
		vote, err := x.m[i].ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx, InputCertificates: proofs})
		if err != nil {
			t.Fatal("child approval", i, err)
		}
		p.Certificate.Summary = vote.Summary
		p.Certificate.QC.Fact = vote.Summary.Fact()
		p.Certificate.QC.Votes = append(p.Certificate.QC.Votes, vote.Vote)
	}
	return p
}

func rejectBoundary(t *testing.T, x *reclaimLab, p protocol.DirectPayment, at int64) {
	t.Helper()
	before := boundaryRows(t, x.ledger)
	raw, err := p.Submission().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	err = x.ledger.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, err := x.engine.ExecuteAt(v, raw, committee.BlockContext{Height: 3, Time: time.Unix(at, 0)})
		return tr.Changes, err
	})
	if err == nil {
		t.Fatal("expired first use accepted")
	}
	if !errors.Is(err, protocol.ErrExpired) {
		t.Fatal("first-use rejection had unrelated cause", err)
	}
	if !reflect.DeepEqual(before, boundaryRows(t, x.ledger)) {
		t.Fatal("rejection changed public state")
	}
	t.Logf("committee rejection at %d: %v", at, err)
}

func TestApprovalExpiryBoundaryHiddenThirdVoteRotations(t *testing.T) {
	for hidden := 0; hidden < 4; hidden++ {
		t.Run(fmt.Sprintf("hidden_GM%d", hidden), func(t *testing.T) {
			x, start, d := boundaryLab(t, 900)
			initial := boundaryBlock(t, x, 1, nil, start)
			followBoundary(t, x, initial, 0, 1, 2, 3)
			indices := []int{}
			for i := 0; i < 4; i++ {
				if i != hidden {
					indices = append(indices, i)
				}
			}
			// Two visible signatures and a genuine third kept private until after expiry.
			indices = append(indices[:2], hidden)
			p := x.approve(t, expiringApprovalTx(t, x, 0, 1, d), indices...)
			if err := p.Certificate.Verify(x.f.Org); err != nil {
				t.Fatal("assembled QC", err)
			}
			child := boundaryChild(t, x, p, d+30, 2)
			boundarySnapshot(t, x, p, "signed_but_unpublished", start)
			expired := boundaryBlock(t, x, 2, initial.Hash(), d)
			followBoundary(t, x, expired, 0, 1, 2, 3)
			rejectBoundary(t, x, p, d)
			rejectBoundary(t, x, child, d)
			boundarySnapshot(t, x, p, "both_first_use_paths_rejected", d)
		})
	}
}

func TestApprovalExpiryBoundarySplitLagAndReuse(t *testing.T) {
	x, start, d := boundaryLab(t, 150)
	var initialSlices [4][]state.Slice
	for i, m := range x.m {
		for _, g := range x.f.Genesis.Grants {
			q, err := m.Quota(g.Key, 0)
			if err != nil {
				t.Fatal(err)
			}
			initialSlices[i] = append(initialSlices[i], q)
		}
	}
	initial := boundaryBlock(t, x, 1, nil, start)
	followBoundary(t, x, initial, 0, 1, 2, 3)
	a := x.approve(t, expiringApprovalTx(t, x, 0, 1, d), 0, 1)
	b := x.approve(t, expiringApprovalTx(t, x, 1, 2, d), 2, 3)
	for i, m := range x.m {
		for _, g := range x.f.Genesis.Grants {
			if g.Key.Kind == protocol.ResourceFUEL {
				q, err := m.Quota(g.Key, 0)
				if err != nil || q.Reserved == 0 {
					t.Fatal("FUEL reservation must be nonzero", i, q, err)
				}
			}
		}
	}
	before := boundaryBlock(t, x, 2, initial.Hash(), d-1)
	followBoundary(t, x, before, 0, 1, 2, 3)
	for i, m := range x.m {
		old := a
		if i >= 2 {
			old = b
		}
		if err := x.db[i].View(func(v state.ReadView) error {
			approval, found, err := state.Load[state.Approval](v, state.Key(state.KeyApproval, old.Certificate.QC.Fact[:]))
			if err != nil {
				return err
			}
			if !found || len(approval.Debits) == 0 {
				t.Fatal("missing real approval debits")
			}
			for _, debit := range approval.Debits {
				q, err := m.Quota(debit.Key, debit.Worker)
				if err != nil || q.Reserved != debit.Cap {
					t.Fatal("early resource release", i, debit, q, err)
				}
			}
			lock, found, err := state.Load[state.Spend](v, rules.DirectSpendKey(old.Tx.Body.Inputs[0].Output, 0))
			if err != nil {
				return err
			}
			if !found || lock.Candidate != old.Certificate.QC.Fact {
				t.Fatal("input unlocked early", i, lock)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	boundarySnapshot(t, x, a, "deadline_minus_1", d-1)
	due := boundaryBlock(t, x, 3, before.Hash(), d)
	followBoundary(t, x, due, 0, 1)
	for i, m := range x.m {
		q, err := m.Quota(x.f.Genesis.Grants[0].Key, 0)
		want := uint64(100)
		if i < 2 {
			want = 0
		}
		if err != nil || q.Reserved != want {
			t.Fatal("lag sync", i, q, err)
		}
	}
	boundarySnapshot(t, x, a, "two_GMs_lagging", d)
	followBoundary(t, x, due, 2, 3)
	for i, m := range x.m {
		old := a
		if i >= 2 {
			old = b
		}
		var approval state.Approval
		if err := x.db[i].View(func(v state.ReadView) error {
			var err error
			var found bool
			approval, found, err = state.Load[state.Approval](v, state.Key(state.KeyApproval, old.Certificate.QC.Fact[:]))
			if err == nil && (!found || len(approval.Debits) == 0) {
				t.Fatal("missing approval debits")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		for _, debit := range approval.Debits {
			q, err := m.Quota(debit.Key, debit.Worker)
			if err != nil || q.Reserved != 0 {
				t.Fatal("resource not released", i, debit, q, err)
			}
		}
		for j, g := range x.f.Genesis.Grants {
			q, err := m.Quota(g.Key, 0)
			if err != nil || q != initialSlices[i][j] {
				t.Fatal("expiry did not restore exact resource slice", i, g.Key.Kind, q, initialSlices[i][j], err)
			}
		}
		if _, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: old.Tx}); err == nil {
			t.Fatal("old approval refreshed")
		}
	}
	after := boundaryBlock(t, x, 4, due.Hash(), d+1)
	followBoundary(t, x, after, 0, 1, 2, 3)
	snapshots := []any{}
	for _, db := range x.db {
		snapshots = append(snapshots, boundaryRows(t, db))
	}
	followBoundary(t, x, after, 0, 1, 2, 3)
	for i, db := range x.db {
		if !reflect.DeepEqual(snapshots[i], boundaryRows(t, db)) {
			t.Fatal("duplicate sync changed state")
		}
	}
	boundarySnapshot(t, x, a, "all_released_once", d+1)
	// The input is actually reused by a new signed request after confirmed expiry.
	for i := range x.m {
		index := 0
		if i >= 2 {
			index = 1
		}
		tx := expiringApprovalTx(t, x, index, uint64(10+i), d+30)
		if _, err := x.m[i].ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx}); err != nil {
			t.Fatal("unlocked input not reusable", i, err)
		}
	}
	boundarySnapshot(t, x, a, "inputs_reapproved_for_new_requests", d+1)
}

func TestApprovalExpiryBoundaryStrictFirstUseAndPoolDelay(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(map[bool]string{false: "self", true: "as_source"}[source], func(t *testing.T) {
			for _, offset := range []int64{-1, 0, 1} {
				t.Run(fmt.Sprintf("offset_%d", offset), func(t *testing.T) {
					x, start, d := boundaryLab(t, 450)
					initial := boundaryBlock(t, x, 1, nil, start)
					followBoundary(t, x, initial, 0, 1, 2, 3)
					p := x.approve(t, expiringApprovalTx(t, x, 0, 1, d), 0, 1, 2)
					if source {
						p = boundaryChild(t, x, p, d+30, 2)
					}
					// Admission/cryptographic verification occurs before D_T; execution rechecks time.
					raw, err := p.Submission().MarshalBinary()
					if err != nil {
						t.Fatal(err)
					}
					app, err := committee.NewTimedApp("reclaim", x.ledger, x.engine.Check, x.engine.ExecuteAt)
					if err != nil {
						t.Fatal(err)
					}
					checked, err := app.CheckTx(context.Background(), &abci.RequestCheckTx{Tx: raw})
					if err != nil || checked.Code != 0 {
						t.Fatal("ABCI pre-deadline admission", checked, err)
					}
					if offset < 0 {
						b := boundaryBlock(t, x, 2, initial.Hash(), d+offset, p)
						followBoundary(t, x, b, 0, 1, 2, 3)
					} else {
						rejectBoundary(t, x, p, d+offset)
					}
					boundarySnapshot(t, x, p, "execution_boundary", d+offset)
				})
			}
		})
	}
}

func TestApprovalExpiryBoundaryDeadlineSignatureBinding(t *testing.T) {
	x, start, d := boundaryLab(t, 450)
	p := x.approve(t, expiringApprovalTx(t, x, 0, 1, d), 0, 1, 2)
	oldID := p.Tx.ID()
	oldFact := p.Certificate.QC.Fact
	p.Tx.Body.ApprovalDeadline++
	if p.Tx.ID() == oldID {
		t.Fatal("deadline not bound to transaction identity")
	}
	if _, err := rules.VerifyDirectPayment(p, x.p); err == nil {
		t.Fatal("deadline mutation retained valid signatures")
	}
	if p.Certificate.QC.Fact != oldFact {
		t.Fatal("test accidentally resigned")
	}
	boundarySnapshot(t, x, p, "mutated_deadline_rejected_with_original_signatures", start)
}

func TestApprovalExpiryBoundaryRegisteredWholeCertificateLateFulfillment(t *testing.T) {
	x, start, d := boundaryLab(t, 900)
	initial := boundaryBlock(t, x, 1, nil, start)
	followBoundary(t, x, initial, 0, 1, 2, 3)
	original := expiringApprovalTx(t, x, 0, 1, d)
	body := original.Body
	body.Outputs = []protocol.Output{body.Outputs[0], body.Outputs[0]}
	body.Outputs[0].Amount = 40
	body.Outputs[1].Amount = 60
	tx, err := protocol.NewFastTx(body, original.Claims, x.p.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), x.f.Owner)}
	parent := x.approve(t, tx, 0, 1, 2)
	child := boundaryChild(t, x, parent, d+30, 2)
	used := boundaryBlock(t, x, 2, initial.Hash(), d-1, child)
	followBoundary(t, x, used, 0, 1, 2, 3)
	var originalSlices [3][]state.Slice
	for i := 0; i < 3; i++ {
		for _, g := range x.f.Genesis.Grants {
			q, err := x.m[i].Quota(g.Key, 0)
			if err != nil {
				t.Fatal(err)
			}
			originalSlices[i] = append(originalSlices[i], q)
		}
	}
	boundarySnapshot(t, x, parent, "whole_certificate_registered_by_one_output", d-1)
	expired := boundaryBlock(t, x, 3, used.Hash(), d)
	followBoundary(t, x, expired, 0, 1, 2, 3)
	for i := 0; i < 3; i++ {
		for j, g := range x.f.Genesis.Grants {
			q, err := x.m[i].Quota(g.Key, 0)
			if err != nil || q != originalSlices[i][j] {
				t.Fatal("registered CAL/FUEL arrangement changed at expiry", i, g.Key.Kind, q, originalSlices[i][j], err)
			}
		}
		if err := x.db[i].View(func(v state.ReadView) error {
			p, _, err := state.Load[member.LocalProgress](v, member.ProgressKey(parent.Certificate.QC.Fact))
			if p.Invalidated {
				t.Fatal("whole registered certificate expired")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	boundarySnapshot(t, x, parent, "deadline_preserved_registered_reservations", d)
	outputBefore := boundaryRows(t, x.ledger)
	if outputBefore == nil {
		t.Fatal("missing ledger")
	}
	late := boundaryBlock(t, x, 4, expired.Hash(), d+1, parent)
	followBoundary(t, x, late, 0, 1, 2, 3)
	var ob rules.DirectObligation
	if err := x.ledger.View(func(v state.ReadView) error {
		var err error
		ob, _, err = state.Load[rules.DirectObligation](v, rules.DirectObligationKey(parent.Certificate.Summary.OutputID(0)))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if ob.Status != rules.DirectFulfilled {
		t.Fatal("late actual committee source did not fulfill", ob)
	}
	for i := 0; i < 4; i++ {
		var charged uint64
		for _, pay := range []protocol.DirectPayment{parent, child} {
			if err := x.db[i].View(func(v state.ReadView) error {
				_, found, err := state.Load[state.Approval](v, state.Key(state.KeyApproval, pay.Certificate.QC.Fact[:]))
				if err != nil || !found {
					return err
				}
				p, found, err := state.Load[member.LocalProgress](v, member.ProgressKey(pay.Certificate.QC.Fact))
				if err != nil {
					return err
				}
				if !found || !p.Settled || p.Pending != 0 || !p.Fee.Closed || p.Fee.Held != 0 {
					t.Fatal("late fulfillment left open local escrow", i, p)
				}
				charged += p.Fee.Rewards + p.Fee.Burned
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		for _, g := range x.f.Genesis.Grants {
			q, err := x.m[i].Quota(g.Key, 0)
			want := uint64(0)
			if g.Key.Kind == protocol.ResourceFUEL || g.Key.Kind == protocol.ResourcePolicy {
				want = charged
			}
			if err != nil || q.Reserved != want {
				t.Fatal("fulfilled reservation does not equal charged fee", i, g.Key.Kind, q, want, err)
			}
		}
	}
	boundarySnapshot(t, x, parent, "late_committee_execution_fulfilled", d+1)
}
