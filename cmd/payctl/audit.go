package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func records[T any](v state.ReadView, kind uint8) ([]T, error) {
	scanner, ok := v.(state.ScanView)
	if !ok {
		return nil, fmt.Errorf("scan unavailable")
	}
	var result []T
	var after []byte
	for {
		rows, e := scanner.Scan(state.Key(kind), after, 512)
		if e != nil {
			return nil, e
		}
		if len(rows) == 0 {
			return result, nil
		}
		for _, row := range rows {
			var x T
			if e = json.Unmarshal(row.Value, &x); e != nil {
				return nil, e
			}
			result = append(result, x)
			after = row.Key
		}
	}
}
func audit(args []string) error {
	flags := flag.NewFlagSet("audit", flag.ContinueOnError)
	dir := flags.String("dir", "", "stopped laboratory directory")
	if e := flags.Parse(args); e != nil {
		return e
	}
	var lab labConfig
	if e := cfg.Read(filepath.Join(*dir, "lab.json"), &lab); e != nil {
		return e
	}
	var network cfg.Network
	if e := cfg.Read(lab.Network, &network); e != nil {
		return e
	}
	type nodeReport struct {
		Name                                 string
		Approvals, Pending, Payments, Closed int
		CAL, FUEL, Rewards, Burned           string
		StateHash, Gap                       string `json:",omitempty"`
		Revisions                            int    `json:",omitempty"`
	}
	var report []nodeReport
	var directState string
	for _, node := range lab.Nodes {
		item := nodeReport{Name: node.Name}
		file := node.Binary + ".db"
		if node.Binary == "committee" {
			file = "committee.db"
		}
		e := store.Inspect(filepath.Join(*dir, node.Name, file), func(v state.ReadView) error {
			outbox, e := records[state.Outbox](v, state.KeyOutbox)
			if e != nil {
				return e
			}
			item.Pending = len(outbox)
			if node.Binary == "member" {
				approvals, e := records[state.Approval](v, state.KeyApproval)
				if e != nil {
					return e
				}
				item.Approvals = len(approvals)
				expected := make(map[protocol.ResourceKey]uint64)
				for _, a := range approvals {
					for _, d := range a.Debits {
						residual, e := protocol.Sub(d.Cap, d.Applied)
						if e != nil {
							return e
						}
						expected[d.Key], e = protocol.Add(expected[d.Key], residual)
						if e != nil {
							return e
						}
					}
				}
				for _, g := range network.Genesis.Grants {
					// Only the member's own grants are present.
					if _, found, e := state.Load[state.Grant](v, state.Key(state.KeyGrant, g.Key.Encode())); e != nil {
						return e
					} else if !found {
						continue
					}
					var available, reserved uint64
					for worker := uint32(0); worker < 256; worker++ {
						slice, found, e := state.Load[state.Slice](v, state.SliceKey(g.Key, worker))
						if e != nil {
							return e
						}
						if !found {
							break
						}
						available, e = protocol.Add(available, slice.Available)
						if e != nil {
							return e
						}
						reserved, e = protocol.Add(reserved, slice.Reserved)
						if e != nil {
							return e
						}
					}
					share, _ := protocol.GrantShare(g.Amount)
					total, e := protocol.Add(available, reserved)
					if e != nil {
						return e
					}
					if total != share || reserved != expected[g.Key] {
						return fmt.Errorf("%s resource %d: share=%d available=%d reserved=%d original residual=%d", node.Name, g.Key.Kind, share, available, reserved, expected[g.Key])
					}
				}
			}
			if node.Binary == "committee" {
				if network.Direct != nil {
					result, err := auditDirectLedger(v, network)
					if err != nil {
						return err
					}
					item.CAL = fmt.Sprint(result.CAL)
					item.FUEL = fmt.Sprint(result.FUEL)
					item.Gap = fmt.Sprint(result.Gap)
					item.Rewards = fmt.Sprint(result.Rewards)
					item.Burned = fmt.Sprint(result.Burned)
					item.Payments = result.Payments
					item.Closed = result.Closed
					item.StateHash = result.StateHash
					if directState != "" && directState != result.StateHash {
						return fmt.Errorf("committee application states differ")
					}
					directState = result.StateHash
					item.Revisions, err = auditDirectHistory(v, network, filepath.Join(*dir, node.Name, "comet", "data"))
					return err
				}
				var initialCAL, initialFUEL, currentCAL, currentFUEL uint64
				add := func(target *uint64, n uint64) error { var e error; *target, e = protocol.Add(*target, n); return e }
				for _, o := range network.Genesis.Outputs {
					target := &initialCAL
					if o.Output.Asset == protocol.AssetFUEL {
						target = &initialFUEL
					}
					if e = add(target, o.Output.Amount); e != nil {
						return e
					}
				}
				for _, a := range network.Accounts {
					if e = add(&initialFUEL, a.Balance); e != nil {
						return e
					}
				}
				scanner := v.(state.ScanView)
				var after []byte
				for {
					rows, e := scanner.Scan(state.Key(state.KeyCreation), after, 512)
					if e != nil {
						return e
					}
					if len(rows) == 0 {
						break
					}
					for _, row := range rows {
						var creation state.Creation
						if e = json.Unmarshal(row.Value, &creation); e != nil {
							return e
						}
						// Last key component is the fixed OutputID.
						id := row.Key[len(row.Key)-32:]
						spend, _, e := state.Load[state.Spend](v, state.Key(state.KeySpend, id))
						if e != nil {
							return e
						}
						if spend.Consumed == (protocol.SpendFactID{}) {
							target := &currentCAL
							if creation.Output.Asset == protocol.AssetFUEL {
								target = &currentFUEL
							}
							if e = add(target, creation.Output.Amount); e != nil {
								return e
							}
						}
						after = row.Key
					}
				}
				balances, e := records[uint64](v, state.KeyAccount)
				if e != nil {
					return e
				}
				for _, balance := range balances {
					if e = add(&currentFUEL, balance); e != nil {
						return e
					}
				}
				payments, e := records[rules.PublicPayment](v, state.KeyPayment)
				if e != nil {
					return e
				}
				item.Payments = len(payments)
				for _, payment := range payments {
					if e = payment.Fee.Validate(); e != nil {
						return e
					}
					if payment.Fee.Closed {
						item.Closed++
					}
					if e = add(&currentFUEL, payment.Fee.Held); e != nil {
						return e
					}
				}
				rewards, e := records[uint64](v, state.KeyReward)
				if e != nil {
					return e
				}
				var rewardTotal uint64
				for _, amount := range rewards {
					if e = add(&rewardTotal, amount); e != nil {
						return e
					}
				}
				burned, _, e := state.Load[uint64](v, state.Key(state.KeyBurned))
				if e != nil {
					return e
				}
				if e = add(&currentFUEL, rewardTotal); e != nil {
					return e
				}
				if e = add(&currentFUEL, burned); e != nil {
					return e
				}
				if initialCAL != currentCAL || initialFUEL != currentFUEL {
					return fmt.Errorf("%s supply mismatch CAL %d/%d FUEL %d/%d", node.Name, currentCAL, initialCAL, currentFUEL, initialFUEL)
				}
				item.CAL = fmt.Sprint(currentCAL)
				item.FUEL = fmt.Sprint(currentFUEL)
				item.Rewards = fmt.Sprint(rewardTotal)
				item.Burned = fmt.Sprint(burned)
			}
			return nil
		})
		if e != nil {
			return fmt.Errorf("%s: %w", node.Name, e)
		}
		report = append(report, item)
	}
	path := filepath.Join(*dir, "reports", "audit.json")
	if e := cfg.Write(path, report); e != nil {
		return e
	}
	raw, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(raw))
	fmt.Println("Supply and original-debit accounting verified:", path)
	return nil
}
