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

const MaxProgressBatch = 128

func directStatus(v state.ReadView, f protocol.SpendFactID) (s DirectStatus, err error) {
	s.Observed, _, err = state.Load[bool](v, state.Key(state.KeyObserved, f[:]))
	if err != nil {
		return s, err
	}
	_, err = v.Get(state.Key(state.KeyApproval, f[:]))
	s.Signed = err == nil
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return s, err
	}
	p, _, err := state.Load[LocalProgress](v, ProgressKey(f))
	s.Closed = p.Fee.Closed
	s.Height = p.Height
	return s, err
}
func (m *Member) DirectStatus(f protocol.SpendFactID) (s DirectStatus, err error) {
	err = m.db.View(func(v state.ReadView) error { s, err = directStatus(v, f); return err })
	return
}

// DirectStatuses reads a bounded group in one snapshot. Results preserve input
// order, including duplicates and unknown facts; no height implies completion.
func (m *Member) DirectStatuses(facts []protocol.SpendFactID) ([]DirectStatus, error) {
	if len(facts) == 0 || len(facts) > MaxProgressBatch {
		return nil, protocol.ErrRule
	}
	out := make([]DirectStatus, len(facts))
	err := m.db.View(func(v state.ReadView) error {
		for i, f := range facts {
			s, err := directStatus(v, f)
			if err != nil {
				return err
			}
			out[i] = s
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
