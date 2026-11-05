package member

import (
	"errors"
	"sync"
	"utxo/internal/state"
	"utxo/protocol"
)

var ErrParentApprovalMissing = errors.New("parent approval unavailable")

// Only summaries of locally committed approvals enter this bounded cache.
// It carries no quorum-authentication result and does not authorize spending.
type approvalSummaryCache struct {
	mu      sync.Mutex
	limit   int
	entries map[protocol.SpendFactID]protocol.OutputSummary
	order   []protocol.SpendFactID
}

func cloneSummary(s protocol.OutputSummary) protocol.OutputSummary {
	s.Outputs = append([]protocol.OutputCommitment(nil), s.Outputs...)
	s.Admission = append(protocol.AdmissionVector(nil), s.Admission...)
	s.Grants = append([]protocol.Hash(nil), s.Grants...)
	return s
}
func (c *approvalSummaryCache) put(s protocol.OutputSummary) {
	if c == nil || c.limit <= 0 {
		return
	}
	fact := s.Fact()
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[fact]; ok {
		return
	}
	if len(c.entries) >= c.limit {
		delete(c.entries, c.order[0])
		c.order = c.order[1:]
	}
	c.entries[fact] = cloneSummary(s)
	c.order = append(c.order, fact)
}
func (c *approvalSummaryCache) get(f protocol.SpendFactID) (protocol.OutputSummary, bool) {
	if c == nil || c.limit <= 0 {
		return protocol.OutputSummary{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.entries[f]
	return cloneSummary(s), ok
}
func (m *Member) decodeContinuation(raw []byte) (protocol.DirectRequest, error) {
	r, err := protocol.DecodeDirectContinuationRequest(raw)
	if err != nil {
		return protocol.DirectRequest{}, err
	}
	cfg := m.cfg.Organization
	if r.Tx.Body.Config != cfg.Hash() || r.Tx.Body.Network != cfg.Network || r.Tx.Body.Certifier != cfg.Org || r.Tx.Body.Epoch != cfg.Epoch {
		return protocol.DirectRequest{}, protocol.ErrAuth
	}
	req := protocol.DirectRequest{Tx: r.Tx}
	for _, p := range r.Parents {
		s, found := m.approvalSummaries.get(p.QC.Fact)
		if !found {
			err = m.db.View(func(v state.ReadView) error {
				a, ok, e := state.Load[state.Approval](v, state.Key(state.KeyApproval, p.QC.Fact[:]))
				if e != nil {
					return e
				}
				if !ok || a.Direct == nil {
					return ErrParentApprovalMissing
				}
				s = protocol.SummaryFor(*a.Direct, a.Admission)
				if a.Fact != p.QC.Fact || s.Fact() != p.QC.Fact {
					return protocol.ErrAuth
				}
				return nil
			})
			if err != nil {
				return protocol.DirectRequest{}, err
			}
		}
		if s.Config != cfg.Hash() || s.Fact() != p.QC.Fact {
			return protocol.DirectRequest{}, protocol.ErrAuth
		}
		// Existing parent validation below authenticates all QC votes and outputs.
		req.InputCertificates = append(req.InputCertificates, protocol.InputCertificate{Certificate: protocol.OutputCertificate{Summary: s, QC: p.QC}, Index: p.Index})
	}
	return req, nil
}
