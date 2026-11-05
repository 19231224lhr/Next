package main

import (
	"context"
	"testing"
	"time"
	"utxo/internal/member"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestWalletDefaultDeadlineAndThreeGMSignatures(t *testing.T) {
	_, _, n, f := c3CLIFixture(t)
	n.Direct.RequireApprovalDeadline = true
	p, err := n.Direct.Policy(n.Schedule, n.Organizations)
	if err != nil {
		t.Fatal(err)
	}
	origin := n.Genesis.Outputs[0]
	start := time.Now().Unix()
	req, err := budgetRequest(n, p, f.Org, f.Owner, protocol.Input{Kind: protocol.FinalInput, Output: origin.ID, Evidence: origin.Fact}, origin.Output, origin.Output.Recipient, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.Tx.Body.ApprovalDeadline < start+protocol.ApprovalLifetimeSeconds || req.Tx.Body.ApprovalDeadline > time.Now().Unix()+protocol.ApprovalLifetimeSeconds {
		t.Fatal("default D_T missing")
	}
	if err := req.Tx.VerifyInitial(p.Key); err != nil {
		t.Fatal(err)
	}
	// The wallet can directly assemble the unchanged 3-of-4 quorum responses.
	cert := protocol.OutputCertificate{}
	for i := 0; i < 3; i++ {
		db := store.NewMemory()
		defer db.Close()
		m, err := member.New(member.Config{Organization: f.Org, Index: uint16(i), Key: f.Keys[i], Peers: []protocol.OrgConfig{f.Org}, Schedule: n.Schedule, Workers: 1, Direct: n.Direct}, db, n.Genesis)
		if err != nil {
			t.Fatal(err)
		}
		a, err := m.ApproveDirect(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		cert.Summary = a.Summary
		cert.QC.Fact = a.Summary.Fact()
		cert.QC.Votes = append(cert.QC.Votes, a.Vote)
		if a.Summary.ApprovalDeadline != req.Tx.Body.ApprovalDeadline {
			t.Fatal("GM did not sign wallet deadline")
		}
		// A retry of identical bytes must neither renew D_T nor reserve twice.
		if _, err := m.ApproveDirect(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if err := db.View(func(v state.ReadView) error {
			approval, found, err := state.Load[state.Approval](v, state.Key(state.KeyApproval, cert.QC.Fact[:]))
			if !found || approval.Direct.Body.ApprovalDeadline != req.Tx.Body.ApprovalDeadline {
				t.Fatal("retry changed deadline")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := cert.Verify(f.Org); err != nil {
		t.Fatal(err)
	}
}
