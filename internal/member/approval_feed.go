package member

import (
	"bytes"
	"utxo/internal/state"
	"utxo/protocol"
)

func approvalCollectionKey(fact protocol.SpendFactID) []byte { return state.Key(239, fact[:]) }

// ApprovedDirect exports only previously committed approvals. The cursor and
// page size bound every read; retries never touch input locks or quota slices.
func (m *Member) ApprovedDirect(after []byte) (protocol.ApprovalPage, error) {
	var page protocol.ApprovalPage
	if m.direct == nil {
		return page, protocol.ErrUnsupported
	}
	prefix := state.Key(239)
	if len(after) != 0 && (!bytes.HasPrefix(after, prefix) || len(after) != len(approvalCollectionKey(protocol.SpendFactID{}))) {
		return page, protocol.ErrEncoding
	}
	err := m.db.View(func(v state.ReadView) error {
		scanner, ok := v.(state.ScanView)
		if !ok {
			return protocol.ErrUnsupported
		}
		rows, err := scanner.Scan(prefix, after, protocol.ApprovalFeedPageSize)
		if err != nil {
			return err
		}
		for _, row := range rows {
			page.Next = bytes.Clone(row.Key)
			d := protocol.NewDecoder(row.Key[len(prefix):])
			raw := d.Bytes(32)
			if d.Done() != nil || len(raw) != 32 {
				return protocol.ErrEncoding
			}
			var fact protocol.SpendFactID
			copy(fact[:], raw)
			a, found, err := state.Load[state.Approval](v, state.Key(state.KeyApproval, fact[:]))
			if err != nil {
				return err
			}
			if !found || a.Direct == nil {
				continue
			}
			p, _, err := state.Load[LocalProgress](v, ProgressKey(fact))
			if err != nil {
				return err
			}
			if p.Invalidated || p.Settled {
				continue
			}
			if _, err := v.Get(state.Key(state.KeyObserved, fact[:])); err == nil {
				continue
			} else if err != state.ErrNotFound {
				return err
			}
			summary := protocol.SummaryFor(*a.Direct, a.Admission)
			if summary.Fact() != fact {
				return protocol.ErrAuth
			}
			page.Items = append(page.Items, protocol.ApprovedDirect{Request: protocol.DirectRequest{Tx: *a.Direct, InputCertificates: a.DirectParents}, Approval: protocol.DirectApproval{Summary: summary, Vote: protocol.SignSpend(fact, m.cfg.Index, m.cfg.Key)}})
		}
		return nil
	})
	return page, err
}
