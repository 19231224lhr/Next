package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/wallet"
	"utxo/protocol"
)

func auditE8(dir string, lab cfg.Lab, n cfg.Network) error {
	var report e8Report
	f, e := os.Open(filepath.Join(dir, "reports", "e8-report.json"))
	if e != nil {
		return e
	}
	e = json.NewDecoder(f).Decode(&report)
	f.Close()
	if e != nil {
		return e
	}
	allSamples := report.Samples
	report.Samples = nil
	for _, s := range allSamples {
		if s.SentNS == 0 && s.Error == "not sent: window ended" {
			continue
		}
		report.Samples = append(report.Samples, s)
	}
	successors := map[protocol.OutputID]protocol.SpendFactID{}
	seenFees := map[protocol.OutputID]bool{}
	for _, s := range report.Samples {
		if s.ReadyNS == 0 {
			return fmt.Errorf("sample %d not ready: %s", s.Index, s.Error)
		}
		if seenFees[s.Fee] {
			return fmt.Errorf("fee reused")
		}
		seenFees[s.Fee] = true
		successors[s.Input] = s.Fact
		if s.Parent >= 0 {
			p := allSamples[s.Parent]
			if p.Output != s.Input || p.Receiver != s.Sender || p.ReadyNS > s.BuildNS || s.Hop != p.Hop+1 {
				return fmt.Errorf("broken dependency %d", s.Index)
			}
		}
	}
	type totals struct {
		Payments, CertificateInputs, MissingResponsibilities, Refunds, ChainEdges int
		Signers                                                                   int
		State                                                                     map[string]map[uint8][2]uint64
	}
	result := totals{Payments: len(report.Samples), State: map[string]map[uint8][2]uint64{}}
	for _, s := range report.Samples {
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
			for _, s := range report.Samples {
				if node.Binary == "member" {
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
	if result.Signers < 3*len(report.Samples) {
		return fmt.Errorf("not enough signers")
	}
	e = store.Inspect(filepath.Join(dir, "e8-wallet.db"), func(v state.ReadView) error {
		sizes, e := e8StateSizes(v)
		if e != nil {
			return e
		}
		result.State["wallet"] = sizes
		for _, s := range report.Samples {
			for _, index := range []uint32{0, protocol.FeeChangeIndex, protocol.FeeRefundIndex} {
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
	return cfg.Write(filepath.Join(dir, "reports", "e8-audit.json"), result)
}
func e8StateSizes(v state.ReadView) (map[uint8][2]uint64, error) {
	scan, ok := v.(state.ScanView)
	if !ok {
		return nil, fmt.Errorf("scan unsupported")
	}
	totals := map[uint8][2]uint64{}
	var after []byte
	for {
		rows, e := scan.Scan(nil, after, 512)
		if e != nil {
			return nil, e
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			x := totals[r.Key[0]]
			x[0]++
			x[1] += uint64(len(r.Key) + len(r.Value))
			totals[r.Key[0]] = x
			after = r.Key
		}
	}
	return totals, nil
}
