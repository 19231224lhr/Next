package committee

import (
	cmttypes "github.com/cometbft/cometbft/types"
	"sort"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

// SetProposalScheduler installs the configured honest-proposer policy. Proposal
// validation cannot infer which valid requests the remote proposer received.
func (a *App) SetProposalScheduler(selectTxs func([][]byte, int64) ([][]byte, error)) {
	a.selectTxs = selectTxs
}

func (e *Engine) SelectProposal(txs [][]byte, capacity int64) ([][]byte, error) {
	if e.direct == nil || !e.direct.FinalC3() {
		return txs, nil
	}
	type candidate struct {
		raw      []byte
		priority uint64
	}
	var reserved, ordinary []candidate
	err := e.db.View(func(v state.ReadView) error {
		for _, raw := range txs {
			if e.Check(raw) != nil {
				continue
			}
			c := candidate{raw: raw}
			closure := protocol.IsCompensationDecision(raw)
			if !closure {
				p, err := protocol.DecodeDirectSubmission(raw)
				if err == nil {
					c.priority = p.Tx.Body.Fee.C3.PriorityReward
					s := p.Summary()
					for i := range s.Outputs {
						ob, found, err := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(s.OutputID(uint32(i))))
						if err != nil {
							return err
						}
						if found && (ob.Status == rules.DirectOpen || ob.Status == rules.DirectRepaired) {
							closure = true
							break
						}
					}
				}
			}
			if closure {
				reserved = append(reserved, c)
			} else {
				ordinary = append(ordinary, c)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(ordinary, func(i, j int) bool { return ordinary[i].priority > ordinary[j].priority })
	quota := capacity/10000*int64(e.direct.C3.ReservedBPS) + (capacity%10000*int64(e.direct.C3.ReservedBPS)+9999)/10000
	out := make([][]byte, 0, len(txs))
	used := int64(0)
	r := 0
	add := func(c candidate) bool {
		n := cmttypes.ComputeProtoSizeForTxs([]cmttypes.Tx{c.raw})
		if n > capacity-used {
			return false
		}
		out = append(out, c.raw)
		used += n
		return true
	}
	for r < len(reserved) && used < quota {
		if !add(reserved[r]) {
			break
		}
		r++
	}
	for _, c := range ordinary {
		add(c)
	}
	for ; r < len(reserved); r++ {
		if !add(reserved[r]) {
			break
		}
	}
	return out, nil
}
