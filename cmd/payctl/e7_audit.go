package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/wallet"
	"utxo/protocol"
)

func auditE7(dir string, lab cfg.Lab, n cfg.Network) error {
	allSamples := map[int]*e7Sample{}
	var samples []*e7Sample
	for _, phase := range []string{"warm", "formal"} {
		for site := 0; site < 2; site++ {
			f, e := os.Open(filepath.Join(dir, "reports", fmt.Sprintf("e7-%s-%d.json", phase, site)))
			if os.IsNotExist(e) && phase == "warm" {
				continue
			}
			if e != nil {
				return e
			}
			var r e7Report
			e = json.NewDecoder(f).Decode(&r)
			f.Close()
			if e != nil {
				return e
			}
			if r.Error != "" {
				return fmt.Errorf("phase: %s", r.Error)
			}
			for _, s := range r.Samples {
				if s.SentNS == 0 && s.Error == "not sent: window ended" {
					continue
				}
				if _, ok := allSamples[s.Index]; ok {
					return fmt.Errorf("duplicate index")
				}
				allSamples[s.Index] = s
				samples = append(samples, s)
			}
		}
	}
	var e error
	signers := map[int]int{}
	successors := map[protocol.OutputID]protocol.SpendFactID{}
	seenFees := map[protocol.OutputID]bool{}
	for _, s := range samples {
		if s.ReadyNS == 0 {
			return fmt.Errorf("sample %d not ready: %s", s.Index, s.Error)
		}
		if seenFees[s.Fee] {
			return fmt.Errorf("fee reused")
		}
		seenFees[s.Fee] = true
		if _, ok := successors[s.Input]; ok {
			return fmt.Errorf("CAL reused")
		}
		successors[s.Input] = s.Fact
		if s.Parent >= 0 {
			p, ok := allSamples[s.Parent]
			if !ok || p.Output != s.Input || p.Receiver != s.Sender || s.ReadyQueueNS < 0 || s.Hop != p.Hop+1 || p.Generation != s.Generation {
				return fmt.Errorf("broken dependency %d", s.Index)
			}
		}
	}
	type totals struct {
		Payments, CertificateInputs, MissingResponsibilities, Refunds, ChainEdges int
		Signers                                                                   int
		State                                                                     map[string]map[uint8][2]uint64
	}
	result := totals{Payments: len(samples), State: map[string]map[uint8][2]uint64{}}
	for _, s := range samples {
		if s.CertificateInput {
			result.CertificateInputs++
		}
		result.MissingResponsibilities += s.Missing
		if s.Parent >= 0 {
			result.ChainEdges++
		}
	}
	for _, node := range lab.Nodes {
		path := filepath.Join(dir, node.Name, node.Binary+".db")
		e = store.Inspect(path, func(v state.ReadView) error {
			sizes, e := e8StateSizes(v)
			if e != nil {
				return e
			}
			result.State[node.Name] = sizes
			if node.Name != "committee0" && node.Binary != "member" {
				return nil
			}
			for _, s := range samples {
				if node.Binary == "member" {
					if !strings.HasPrefix(node.Name, fmt.Sprintf("org%d-", s.Sender%2)) {
						continue
					}
					a, found, e := state.Load[state.Approval](v, state.Key(state.KeyApproval, s.Fact[:]))
					if e != nil {
						return e
					}
					observed, _, e := state.Load[bool](v, state.Key(state.KeyObserved, s.Fact[:]))
					if e != nil {
						return e
					}
					if !observed {
						return fmt.Errorf("member unobserved %d", s.Index)
					}
					if found {
						result.Signers++
						signers[s.Index]++
						applied, e := member.AppliedDebits(v, a)
						if e != nil {
							return e
						}
						for i, d := range a.Debits {
							if applied[i] != d.Cap {
								return fmt.Errorf("member residual %d", s.Index)
							}
						}
					}
					continue
				}
				if s.Parent >= 0 {
					parent := allSamples[s.Parent]
					ob, found, e := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(s.Input))
					if e != nil {
						return e
					}
					if found && (ob.Issuer != n.Organizations[parent.Sender%2].Org || ob.Amount != 100 || ob.Status != rules.DirectFulfilled) {
						return fmt.Errorf("responsibility mismatch %d", s.Index)
					}
				}
				p, ok, e := state.Load[rules.DirectPaymentState](v, state.Key(103, s.Fact[:]))
				if e != nil {
					return e
				}
				if !ok || !p.Fee.Closed || p.FeeSource != protocol.OwnerFinalUTXO || p.Pending != 0 {
					return fmt.Errorf("unsettled payment %d", s.Index)
				}
				for _, id := range []protocol.OutputID{s.Input, s.Fee} {
					spent, ok, e := state.Load[state.Spend](v, rules.DirectSpendKey(id, 0))
					if e != nil {
						return e
					}
					if !ok || spent.Consumed != s.Fact {
						return fmt.Errorf("wrong consumer %d", s.Index)
					}
				}
				c, ok, e := state.Load[state.Creation](v, rules.DirectCreationKey(s.Output, 0))
				if e != nil {
					return e
				}
				if !ok || !c.Final || c.Output.Amount != 100 {
					return fmt.Errorf("output mismatch %d", s.Index)
				}
				spent, _, e := state.Load[state.Spend](v, rules.DirectSpendKey(s.Output, 0))
				if e != nil {
					return e
				}
				if spent.Consumed != successors[s.Output] {
					return fmt.Errorf("chain tip mismatch %d", s.Index)
				}
			}
			return nil
		})
		if e != nil {
			return fmt.Errorf("%s: %w", node.Name, e)
		}
	}
	for _, s := range samples {
		if signers[s.Index] < 3 {
			return fmt.Errorf("not enough signers for %d", s.Index)
		}
	}
	for site := 0; site < 2; site++ {
		e = store.Inspect(filepath.Join(dir, fmt.Sprintf("e7-wallet%d.db", site)), func(v state.ReadView) error {
			sizes, e := e8StateSizes(v)
			if e != nil {
				return e
			}
			result.State[fmt.Sprintf("wallet%d", site)] = sizes
			for _, s := range samples {
				for _, index := range []uint32{0, protocol.FeeChangeIndex, protocol.FeeRefundIndex} {
					owner := s.Sender
					if index == 0 {
						owner = s.Receiver
					}
					if owner%2 != site {
						continue
					}
					id := protocol.OutputIdentity(n.Genesis.Network, s.Tx, index)
					coin, ok, e := state.Load[wallet.DirectCoin](v, wallet.DirectCoinKey(id, 0))
					if e != nil {
						return e
					}
					if !ok || coin.Final == (protocol.Hash{}) {
						return fmt.Errorf("wallet missing final output %d/%d", s.Index, index)
					}
					if index == protocol.FeeRefundIndex {
						result.Refunds++
					}
				}
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	return cfg.Write(filepath.Join(dir, "reports", "e7-audit.json"), result)
}
