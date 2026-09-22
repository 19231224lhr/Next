package main

import (
	"flag"
	"fmt"
	"path/filepath"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type budgetAuditPayment struct {
	Unit                   int
	Role                   string
	Fact                   protocol.SpendFactID
	Ready, Public          bool
	Signers, ClosedSigners int
	Fee                    rules.Escrow
	Obligation             *rules.DirectObligation `json:",omitempty"`
}

// Offline audit uses the stopped stores; it never clears a partial approval.
func auditBudget(args []string) error {
	f := flag.NewFlagSet("budget-audit", flag.ContinueOnError)
	dir := f.String("dir", "", "stopped E2 lab")
	if e := f.Parse(args); e != nil {
		return e
	}
	var lab cfg.Lab
	var report budgetReport
	if e := cfg.Read(filepath.Join(*dir, "lab.json"), &lab); e != nil {
		return e
	}
	if e := cfg.Read(filepath.Join(*dir, "reports", "budget-v4.json"), &report); e != nil {
		return e
	}
	var rows []*budgetAuditPayment
	for _, u := range report.Units {
		for i, h := range []*chainHop{&u.Parent, &u.Child} {
			if h.Fact == (protocol.SpendFactID{}) {
				continue
			}
			role := "parent"
			if i == 1 {
				role = "child"
			}
			rows = append(rows, &budgetAuditPayment{Unit: u.Index, Role: role, Fact: h.Fact, Ready: h.ReadyUnixNS > 0})
		}
	}
	for _, node := range lab.Nodes {
		if node.Binary != "member" && node.Name != "committee0" {
			continue
		}
		path := filepath.Join(*dir, node.Name, node.Binary+".db")
		if e := store.Inspect(path, func(v state.ReadView) error {
			for _, r := range rows {
				if node.Binary == "member" {
					a, found, e := state.Load[state.Approval](v, state.Key(state.KeyApproval, r.Fact[:]))
					if e != nil {
						return e
					}
					if !found {
						continue
					}
					r.Signers++
					p, _, e := state.Load[member.LocalProgress](v, member.ProgressKey(a.Fact))
					if e != nil {
						return e
					}
					if p.Fee.Closed {
						r.ClosedSigners++
					}
					continue
				}
				p, found, e := state.Load[rules.DirectPaymentState](v, state.Key(103, r.Fact[:]))
				if e != nil {
					return e
				}
				r.Public = found
				if !found {
					continue
				}
				r.Fee = p.Fee
				if e = r.Fee.Validate(); e != nil {
					return e
				}
				id := p.Summary.OutputID(0)
				ob, found, e := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(id))
				if e != nil {
					return e
				}
				if found {
					r.Obligation = &ob
				}
			}
			return nil
		}); e != nil {
			return fmt.Errorf("%s: %w", node.Name, e)
		}
	}
	partial := 0
	for _, r := range rows {
		if !r.Ready && r.Signers > 0 {
			partial++
		}
	}
	return cfg.Write(filepath.Join(*dir, "reports", "budget-audit.json"), struct {
		PartialApprovals int
		Payments         []*budgetAuditPayment
	}{partial, rows})
}
