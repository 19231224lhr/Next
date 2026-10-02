//go:build comet_v3

package redaction

import (
	cmtstore "github.com/cometbft/cometbft/store"
	"sort"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

// SelectRepairInputs chooses already-decided, unrepresented targets.
// Economic affordability was enforced by the public decision.
func SelectRepairInputs(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, outputs []protocol.OutputID, now int64) ([]protocol.OutputID, error) {
	if len(outputs) == 0 || len(outputs) > protocol.MaxRepairItems {
		return nil, protocol.ErrRule
	}
	var candidates []protocol.RepairInput
	seen := make(map[protocol.OutputID]bool)
	for _, output := range outputs {
		if seen[output] {
			continue
		}
		seen[output] = true
		c, _, err := InputTarget(v, bs, p, output, now)
		if err == nil {
			candidates = append(candidates, c)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Height != b.Height {
			return a.Height < b.Height
		}
		return a.Transaction < b.Transaction || (a.Transaction == b.Transaction && a.Input < b.Input)
	})
	selected := make([]protocol.OutputID, 0, len(candidates))
	for _, c := range candidates {
		selected = append(selected, c.Output)
	}
	return selected, nil
}
