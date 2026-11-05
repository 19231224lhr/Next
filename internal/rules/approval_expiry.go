package rules

import (
	"utxo/internal/state"
	"utxo/protocol"
)

// CheckApprovalFirstUse uses committed block time. A whole-certificate guarantee
// survives D_T, so its late source execution and compensation remain valid.
func CheckApprovalFirstUse(v state.ReadView, s protocol.OutputSummary, now int64, required bool) error {
	if s.ApprovalDeadline == 0 {
		if required {
			return protocol.ErrRule
		}
		return nil // Historical genesis, with the original rule identifier.
	}
	if now <= 0 {
		return protocol.ErrRule
	}
	if now < s.ApprovalDeadline {
		return nil
	}
	fact := s.Fact()
	for _, prefix := range []byte{keyDirectCoverage, keyDirectPayment} {
		if _, err := v.Get(state.Key(prefix, fact[:])); err == nil {
			return nil
		} else if err != state.ErrNotFound {
			return err
		}
	}
	return protocol.ErrExpired
}
