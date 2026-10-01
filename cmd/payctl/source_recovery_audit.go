package main

import (
	"encoding/hex"
	"encoding/json"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

type reserveRecoveryAudit struct {
	Organization             protocol.Hash
	Balance, Spent, Reserved uint64
}
type sourceRecoveryAudit struct {
	UserCAL                              map[string]uint64
	Open, Fulfilled, Repaired, Recovered int
	PaidCAL, RecoveredCAL                uint64
	Reserves                             []reserveRecoveryAudit
}

// Offline accounting only: sums unique direct obligations, never overlapping
// member budgets, and retains gross debits separately from actual recoveries.
func auditSourceRecovery(v state.ReadView, n cfg.Network) (r sourceRecoveryAudit, err error) {
	r.UserCAL = make(map[string]uint64)
	var cursor []byte
	for {
		rows, e := v.(state.ScanView).Scan(state.Key(state.KeyCreation), cursor, 512)
		if e != nil {
			return r, e
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			var c state.Creation
			if e = json.Unmarshal(row.Value, &c); e != nil {
				return r, e
			}
			if c.Final && c.Output.Asset == protocol.AssetCAL {
				k := append([]byte(nil), row.Key...)
				k[2] = state.KeySpend
				spend, _, e := state.Load[state.Spend](v, k)
				if e != nil {
					return r, e
				}
				if spend.Consumed == (protocol.SpendFactID{}) {
					owner := hex.EncodeToString(c.Output.Recipient.Owner[:])
					r.UserCAL[owner], e = protocol.Add(r.UserCAL[owner], c.Output.Amount)
					if e != nil {
						return r, e
					}
				}
			}
			cursor = row.Key
		}
	}
	obs, err := records[rules.DirectObligation](v, rules.DirectObligationKey(protocol.OutputID{})[2])
	if err != nil {
		return r, err
	}
	for _, ob := range obs {
		switch ob.Status {
		case rules.DirectOpen:
			r.Open++
		case rules.DirectFulfilled:
			r.Fulfilled++
		case rules.DirectRepaired, rules.DirectRecovered:
			r.PaidCAL, err = protocol.Add(r.PaidCAL, ob.Amount)
			if err != nil {
				return r, err
			}
			if ob.Status == rules.DirectRecovered {
				r.Recovered++
				r.RecoveredCAL, err = protocol.Add(r.RecoveredCAL, ob.Amount)
				if err != nil {
					return r, err
				}
			} else {
				r.Repaired++
			}
		default:
			return r, protocol.ErrRule
		}
	}
	for _, org := range n.Organizations {
		x := reserveRecoveryAudit{Organization: org.Org}
		x.Balance, _, err = state.Load[uint64](v, rules.AccountKey(org.Org, protocol.AssetCAL))
		if err != nil {
			return r, err
		}
		for _, g := range n.Genesis.Grants {
			if g.Organization != org.Hash() || g.Key.Kind != protocol.ResourceCAL {
				continue
			}
			u, _, e := state.Load[rules.PublicUsage](v, state.Key(state.KeyUsage, g.Key.Encode()))
			if e != nil {
				return r, e
			}
			x.Spent, err = protocol.Add(x.Spent, u.Spent)
			if err != nil {
				return r, err
			}
			x.Reserved, err = protocol.Add(x.Reserved, u.Reserved)
			if err != nil {
				return r, err
			}
		}
		r.Reserves = append(r.Reserves, x)
	}
	return r, nil
}
