package member

import (
	"errors"
	"utxo/internal/state"
	"utxo/protocol"
)

// DirectStatus is a local experiment observation, never a committee receipt.
type DirectStatus struct {
	Observed, Signed, Closed bool
	Height                   int64
}

func (m *Member) DirectStatus(f protocol.SpendFactID) (s DirectStatus, err error) {
	err = m.db.View(func(v state.ReadView) error {
		var err error
		s.Observed, _, err = state.Load[bool](v, state.Key(state.KeyObserved, f[:]))
		if err != nil {
			return err
		}
		_, err = v.Get(state.Key(state.KeyApproval, f[:]))
		s.Signed = err == nil
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
		p, _, err := state.Load[LocalProgress](v, ProgressKey(f))
		s.Closed = p.Fee.Closed
		s.Height = p.Height
		return err
	})
	return
}
