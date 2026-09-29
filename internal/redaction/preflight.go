//go:build comet_v3

package redaction

import (
	cmtstore "github.com/cometbft/cometbft/store"
	"sort"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

// SelectRepairInputs chooses an affordable subset before any share RPC. The
// overlay is simulation only: qualification always reads the original view.
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
	o := state.NewOverlay(v)
	var selected []protocol.OutputID
	for _, c := range candidates {
		if simulateCompensation(o, p, c.Output, now) == nil {
			selected = append(selected, c.Output)
		}
	}
	return selected, nil
}

func simulateCompensation(o *state.Overlay, p rules.DirectPolicy, output protocol.OutputID, now int64) error {
	tr, err := rules.EvaluateDirectCompensation(o, output, p, now)
	if err != nil {
		return err
	}
	r, err := protocol.DecodeExecution(tr.Data)
	if err != nil {
		return err
	}
	if !r.Applied {
		return rules.ErrConflict
	}
	o.Apply(tr.Changes)
	return nil
}

// Fixed requests cannot be silently shortened by a signing service.
func preflightRepairs(v state.ReadView, p rules.DirectPolicy, outputs []protocol.OutputID, now int64) error {
	o := state.NewOverlay(v)
	for _, output := range outputs {
		if err := simulateCompensation(o, p, output, now); err != nil {
			return err
		}
	}
	return nil
}
