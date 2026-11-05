package member_test

import (
	"context"
	"testing"
	"time"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func expiringApprovalTx(t *testing.T, x *reclaimLab, index int, nonce uint64, deadline int64) protocol.FastTx {
	t.Helper()
	old := x.tx(t, index, nonce, false)
	body := old.Body
	body.ApprovalDeadline = deadline
	tx, err := protocol.NewFastTx(body, old.Claims, x.p.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), x.f.Owner)}
	return tx
}

func expiryBlock(t *testing.T, x *reclaimLab, height int64, previous []byte, at int64) finality.VerifiedBlock {
	t.Helper()
	trust, raw := testkit.Block("reclaim", height, previous, nil, nil, time.Unix(at, 0))
	b, err := finality.VerifyBlock(trust, raw)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range x.m {
		if err := blockfollow.Commit(x.db[i], b, m.PrepareBlock); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func TestApprovalExpiryReleasesSplitApprovalsOnce(t *testing.T) {
	x := newReclaimLab(t)
	deadline := time.Now().Unix() + 60
	// Neither independent transaction collects 3-of-4, and neither input is spent.
	a := x.approve(t, expiringApprovalTx(t, x, 0, 1, deadline), 0, 1)
	b := x.approve(t, expiringApprovalTx(t, x, 1, 2, deadline), 2, 3)
	before := expiryBlock(t, x, 1, nil, deadline-1)
	for _, m := range x.m {
		q, err := m.Quota(x.f.Genesis.Grants[0].Key, 0)
		if err != nil || q.Reserved != 100 {
			t.Fatal("early release", q, err)
		}
	}
	expired := expiryBlock(t, x, 2, before.Hash(), deadline)
	expiryBlock(t, x, 3, expired.Hash(), deadline+1)
	for i, m := range x.m {
		q, err := m.Quota(x.f.Genesis.Grants[0].Key, 0)
		if err != nil || q.Reserved != 0 || q.Available != 100 {
			t.Fatal("expiry did not release once", i, q, err)
		}
		pay := a
		if i >= 2 {
			pay = b
		}
		if _, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: pay.Tx}); err == nil {
			t.Fatal("expired retry voted again")
		}
		if err := x.db[i].View(func(v state.ReadView) error {
			s, _, e := state.Load[state.Spend](v, rules.DirectSpendKey(pay.Tx.Body.Inputs[0].Output, 0))
			if e == nil && s.Candidate != (protocol.SpendFactID{}) {
				t.Fatal("input lock retained")
			}
			p, _, e := state.Load[member.LocalProgress](v, member.ProgressKey(pay.Certificate.QC.Fact))
			if e == nil && !p.Invalidated {
				t.Fatal("expiry not recorded")
			}
			return e
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestApprovalExpiryReclaimsInstalledButUnpublished(t *testing.T) {
	x := newReclaimLab(t)
	deadline := time.Now().Unix() + 60
	pay := x.approve(t, expiringApprovalTx(t, x, 0, 1, deadline), 0, 1, 2)
	if err := x.m[0].InstallDirect(pay); err != nil {
		t.Fatal(err)
	}
	expiryBlock(t, x, 1, nil, deadline)
	if err := x.db[0].View(func(v state.ReadView) error {
		for _, prefix := range []byte{state.KeyOutbox, state.KeyInstall} {
			if _, e := v.Get(state.Key(prefix, pay.Certificate.QC.Fact[:])); e != state.ErrNotFound {
				t.Fatal("expired relay retained", e)
			}
		}
		s, _, err := state.Load[state.Spend](v, rules.DirectSpendKey(pay.Tx.Body.Inputs[0].Output, 0))
		if s.Candidate != (protocol.SpendFactID{}) || s.Consumed != (protocol.SpendFactID{}) {
			t.Fatal("unpublished install lock retained")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := x.m[3].InstallDirect(pay); err == nil {
		t.Fatal("late installation accepted on non-signer")
	}
}

func TestApprovalExpiryKeepsWholeRegisteredCertificate(t *testing.T) {
	x := newReclaimLab(t, 450)
	for i := range x.m {
		x.cfg[i].DisableSourceRecovery = true
		var err error
		x.m[i], err = member.New(x.cfg[i], x.db[i], x.f.Genesis)
		if err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Unix() + 60
	a := x.approve(t, expiringApprovalTx(t, x, 0, 1, deadline), 0, 1, 2)
	base := x.tx(t, 1, 2, false)
	body := base.Body
	body.ApprovalDeadline = deadline + 1
	body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: a.Certificate.Summary.OutputID(0), Evidence: protocol.Hash(a.Certificate.QC.Fact)}}
	tx, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: a.Tx.Body.Outputs[0]}}, x.p.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), x.f.Owner)}
	proofs := []protocol.InputCertificate{{Certificate: a.Certificate, Index: 0}}
	child := protocol.DirectPayment{Tx: tx, InputCertificates: proofs}
	for _, i := range []int{1, 2, 3} {
		v, err := x.m[i].ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx, InputCertificates: proofs})
		if err != nil {
			t.Fatal(err)
		}
		child.Certificate.Summary = v.Summary
		child.Certificate.QC.Fact = v.Summary.Fact()
		child.Certificate.QC.Votes = append(child.Certificate.QC.Votes, v.Vote)
	}
	// Registration must not depend on whether background source recovery is enabled.
	// This successful public child execution is the authenticated evidence.
	b := x.block(t, child)
	for i, m := range x.m {
		if err := blockfollow.Commit(x.db[i], b, m.PrepareBlock); err != nil {
			t.Fatal(err)
		}
	}
	expiryBlock(t, x, 2, b.Hash(), deadline+2)
	for i := 0; i < 3; i++ {
		if err := x.db[i].View(func(v state.ReadView) error {
			p, _, err := state.Load[member.LocalProgress](v, member.ProgressKey(a.Certificate.QC.Fact))
			if p.Invalidated {
				t.Fatal("registered guarantee expired")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		q, err := x.m[i].Quota(x.f.Genesis.Grants[0].Key, 0)
		if err != nil || q.Reserved != 100 {
			t.Fatal("source guarantee released", q, err)
		}
	}
	if err := x.m[3].InstallDirect(a); err != nil {
		t.Fatal("registered late source rejected", err)
	}
}

func TestApprovalExpiryRejectsExpiredOrUnboundedRequest(t *testing.T) {
	x := newReclaimLab(t)
	now := time.Now().Unix()
	for _, deadline := range []int64{now - 1, now + protocol.ApprovalLifetimeSeconds + 600} {
		tx := expiringApprovalTx(t, x, 0, 1, deadline)
		if _, err := x.m[0].ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx}); err == nil {
			t.Fatal("invalid lifetime accepted")
		}
	}
	q, err := x.m[0].Quota(x.f.Genesis.Grants[0].Key, 0)
	if err != nil || q.Reserved != 0 {
		t.Fatal("invalid request reserved quota", q, err)
	}
}
