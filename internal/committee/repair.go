//go:build comet_v3

package committee

import (
	cmtstore "github.com/cometbft/cometbft/store"
	"utxo/internal/redaction"
	"utxo/internal/state"
	"utxo/protocol"
)

// EnableRepair is configured before starting ABCI. Ordinary v2 nodes do not
// expose this command. Physical block writes happen separately after Commit.
func (e *Engine) EnableRepair(blocks *cmtstore.BlockStore) error {
	if e.direct == nil || blocks == nil {
		return protocol.ErrRule
	}
	e.repairCheck = func(raw []byte) error {
		c, err := protocol.DecodeRepairInput(raw)
		if err != nil {
			return err
		}
		if c.Network != e.cfg.Network {
			return protocol.ErrAuth
		}
		return nil
	}
	e.repairExecute = func(v state.ReadView, raw []byte, b BlockContext) (state.Transition, error) {
		c, err := protocol.DecodeRepairInput(raw)
		if err != nil {
			return state.Transition{}, err
		}
		return redaction.Execute(v, blocks, *e.direct, c, b.Height, b.Time.Unix())
	}
	return nil
}
