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
	"utxo/internal/wallet"
	"utxo/protocol"
)

type budgetAuditPayment struct {
	FeeSource                      protocol.FeeSource
	FeeInput                       protocol.OutputID
	Transaction                    protocol.TxID
	FeeInputAmount, Change, Refund uint64
	WalletRefundFinal              bool
	FeeLockedMembers               int
	WalletFeeLocked                bool

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
	var network cfg.Network
	if e := cfg.Read(lab.Network, &network); e != nil {
		return e
	}
	origins := make(map[protocol.OutputID]state.OriginOutput, len(network.Genesis.Outputs))
	for _, o := range network.Genesis.Outputs {
		origins[o.ID] = o
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
			fee := u.ParentFee
			if i == 1 {
				fee = u.ChildFee
			}
			rows = append(rows, &budgetAuditPayment{FeeInputAmount: origins[fee].Output.Amount, FeeInput: fee, Transaction: h.Tx, Unit: u.Index, Role: role, Fact: h.Fact, Ready: h.ReadyUnixNS > 0})
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
					if report.OwnerFuel {
						spend, _, e := state.Load[state.Spend](v, rules.DirectSpendKey(r.FeeInput, 0))
						if e != nil {
							return e
						}
						if spend.Candidate == r.Fact || spend.Consumed == r.Fact {
							r.FeeLockedMembers++
						}
					}

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
				r.FeeSource = p.FeeSource
				if report.OwnerFuel {
					if p.FeeSource != protocol.OwnerFinalUTXO {
						return fmt.Errorf("organization paid owner experiment")
					}
					origin, ok := origins[r.FeeInput]
					if !ok || origin.Output.Asset != protocol.AssetFUEL {
						return protocol.ErrRule
					}
					r.FeeInputAmount = origin.Output.Amount
					spent, _, e := state.Load[state.Spend](v, rules.DirectSpendKey(r.FeeInput, 0))
					if e != nil {
						return e
					}
					if spent.Consumed != r.Fact {
						return fmt.Errorf("fee input not consumed")
					}
					for index, target := range map[uint32]*uint64{protocol.FeeChangeIndex: &r.Change, protocol.FeeRefundIndex: &r.Refund} {
						id := protocol.OutputIdentity(network.Genesis.Network, r.Transaction, index)
						c, found, e := state.Load[state.Creation](v, rules.DirectCreationKey(id, 0))
						if e != nil {
							return e
						}
						if found {
							if !c.Final || c.Output.Asset != protocol.AssetFUEL || c.Output.Recipient != p.FeeRefund {
								return protocol.ErrAuth
							}
							*target = c.Output.Amount
						}
					}
					if r.Change != r.FeeInputAmount-p.Fee.Maximum || r.Refund != p.Fee.Refunded {
						return fmt.Errorf("incorrect owner change/refund")
					}
				}
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
	if report.OwnerFuel {
		for i := 0; i < 2; i++ {
			if e := store.Inspect(filepath.Join(*dir, fmt.Sprintf("budget-wallet-%d.db", i)), func(v state.ReadView) error {
				for _, r := range rows {
					if (i == 0 && r.Role != "parent") || (i == 1 && r.Role != "child") {
						continue
					}
					locked, found, e := state.Load[protocol.TxID](v, state.Key(state.KeyWalletSpend, r.FeeInput[:], []byte{0}))
					if e != nil {
						return e
					}
					r.WalletFeeLocked = found && locked == r.Transaction
					if !r.Public || r.Refund == 0 {
						continue
					}
					id := protocol.OutputIdentity(network.Genesis.Network, r.Transaction, protocol.FeeRefundIndex)
					c, ok, e := state.Load[wallet.DirectCoin](v, wallet.DirectCoinKey(id, 0))
					if e != nil {
						return e
					}
					r.WalletRefundFinal = ok && c.Final != (protocol.Hash{}) && c.Output.Asset == protocol.AssetFUEL && c.Output.Amount == r.Refund
					if !r.WalletRefundFinal {
						return fmt.Errorf("wallet missed public refund for unit %d %s", r.Unit, r.Role)
					}
				}
				return nil
			}); e != nil {
				return e
			}
		}
		if e := store.Inspect(filepath.Join(*dir, "committee0", "committee.db"), func(v state.ReadView) error {
			for _, org := range network.Organizations {
				balance, _, e := state.Load[uint64](v, rules.AccountKey(org.Org, protocol.AssetFUEL))
				if e != nil {
					return e
				}
				if balance != 0 {
					return fmt.Errorf("organization FUEL account nonzero")
				}
			}
			for _, a := range network.Accounts {
				if a.Asset == protocol.AssetFUEL && a.Balance != 0 {
					return fmt.Errorf("organization funded at genesis")
				}
			}

			for _, g := range network.Genesis.Grants {
				if g.Key.Kind == protocol.ResourceFUEL || g.Key.Kind == protocol.ResourcePolicy {
					return fmt.Errorf("sponsorship grant present")
				}
			}
			return nil
		}); e != nil {
			return e
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
