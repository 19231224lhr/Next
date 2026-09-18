package rules

import (
	"encoding/json"
	"math"
	"utxo/internal/state"
	"utxo/protocol"
)

func DirectAnchorKey(height int64, id protocol.OutputID) []byte {
	e := new(protocol.Encoder)
	e.U64(uint64(height))
	e.Fixed(id[:])
	return state.Key(111, e.Data())
}

// AnchorDirectDeadlines runs before a block's commands. Tendermint block H+1
// carries the median commit time of H, so idle time before H cannot shorten a
// responsibility opened in H. This never delays H's payment confirmation.
func AnchorDirectDeadlines(v state.ReadView, p DirectPolicy, height, now int64) (state.Transition, error) {
	if now <= 0 || p.TimeoutSeconds <= 0 || now > math.MaxInt64-p.TimeoutSeconds {
		return state.Transition{}, protocol.ErrRule
	}
	scanner, ok := v.(state.ScanView)
	if !ok {
		return state.Transition{}, protocol.ErrRule
	}
	o := state.NewOverlay(v)
	var cursor []byte
	for {
		entries, err := scanner.Scan(state.Key(111), cursor, 1024)
		if err != nil {
			return state.Transition{}, err
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			var queued DirectObligation
			if err = json.Unmarshal(entry.Value, &queued); err != nil {
				return state.Transition{}, err
			}
			if queued.AnchorHeight > height {
				return state.Transition{Changes: o.Changes()}, nil
			}
			ob, found, err := state.Load[DirectObligation](o, DirectObligationKey(queued.Output))
			if err != nil {
				return state.Transition{}, err
			}
			if found && ob.Status == DirectOpen && ob.Deadline == 0 {
				ob.Deadline = now + p.TimeoutSeconds
				if err = state.Put(o, DirectObligationKey(ob.Output), ob); err != nil {
					return state.Transition{}, err
				}
				if err = state.Put(o, DirectDueKey(ob.Deadline, ob.Output), ob); err != nil {
					return state.Transition{}, err
				}
			}
			o.Apply([]state.Change{{Key: entry.Key, Delete: true}})
			cursor = entry.Key
		}
	}
	return state.Transition{Changes: o.Changes()}, nil
}
