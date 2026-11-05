package gateway

import (
	"bytes"
	"context"
	"errors"
	"time"
	"utxo/internal/state"
	"utxo/protocol"
)

type ApprovalSource interface {
	ApprovedDirect(context.Context, []byte) (protocol.ApprovalPage, error)
}

type pendingApproval struct {
	request protocol.DirectRequest
	summary protocol.OutputSummary
	votes   [4]*protocol.SpendVote
}

type ApprovalCollector struct {
	collector *Collector
	sources   [4]ApprovalSource
	// after[omitted] is the common cursor for the other three sources.
	after   [4][]byte
	pending map[protocol.SpendFactID]*pendingApproval
}

func NewApprovalCollector(c *Collector, sources [4]ApprovalSource) *ApprovalCollector {
	return &ApprovalCollector{collector: c, sources: sources}
}

// approvalPageCovers checks the stream boundary, independently of authenticating
// individual votes. Empty Next denotes EOF and therefore covers the remaining range.
func approvalPageCovers(page protocol.ApprovalPage, after []byte) bool {
	if len(page.Items) > protocol.ApprovalFeedPageSize {
		return false
	}
	if len(page.Next) == 0 {
		return len(page.Items) == 0
	}
	prefix := state.Key(239)
	if len(page.Next) != len(state.Key(239, make([]byte, 32))) || !bytes.HasPrefix(page.Next, prefix) || bytes.Compare(page.Next, after) <= 0 {
		return false
	}
	for _, item := range page.Items {
		fact := item.Approval.Summary.Fact()
		key := state.Key(239, fact[:])
		if bytes.Compare(key, after) <= 0 || bytes.Compare(key, page.Next) > 0 {
			return false
		}
	}
	return true
}

// Poll scans every three-member subset independently. A faulty source can pin
// the lanes containing it, but cannot control the all-honest lane's watermark.
// Requests sharing a source and cursor are coalesced: four RPCs when aligned,
// at most twelve otherwise. Pages and per-lane vote groups stay bounded.
// Only committed GM records are read; wallet approval and input locks are untouched.
func (b *ApprovalCollector) Poll(ctx context.Context) error {
	// Scratch state must not survive a failed persistence attempt either.
	defer func() { b.pending = nil }()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	type request struct {
		index int
		after string
	}
	type answer struct {
		request request
		page    protocol.ApprovalPage
		err     error
	}
	requests := make(map[request]struct{}, 12)
	for omitted, after := range b.after {
		for index, source := range b.sources {
			if index != omitted && source != nil {
				requests[request{index, string(after)}] = struct{}{}
			}
		}
	}
	answers := make(chan answer, len(requests))
	for r := range requests {
		go func(r request) {
			page, err := b.sources[r.index].ApprovedDirect(ctx, []byte(r.after))
			answers <- answer{r, page, err}
		}(r)
	}
	pages := make(map[request]*protocol.ApprovalPage, len(requests))
collect:
	for range requests {
		select {
		case <-ctx.Done():
			break collect
		case a := <-answers:
			if a.err == nil && approvalPageCovers(a.page, []byte(a.request.after)) {
				// The same response can serve three lanes. Authenticate each item
				// once, retaining the stream boundary even when rows are invalid.
				page := protocol.ApprovalPage{Next: a.page.Next}
				for _, item := range a.page.Items {
					if protocol.ValidateDirectApproval(b.collector.org, item.Request, item.Approval, a.request.index) != nil {
						continue
					}
					if _, err := item.Request.MarshalBinary(); err != nil {
						continue
					}
					page.Items = append(page.Items, item)
				}
				pages[a.request] = &page
			}
		}
	}
	// Even when one source times out, merge any complete honest triple.
	progress := false
	for omitted, after := range b.after {
		var triple [4]*protocol.ApprovalPage
		complete := true
		for index := range b.sources {
			if index == omitted {
				continue
			}
			triple[index] = pages[request{index, string(after)}]
			if triple[index] == nil {
				complete = false
			}
		}
		if !complete {
			continue
		}
		if err := b.mergeApprovalLane(omitted, triple); err != nil {
			return err
		}
		progress = true
	}
	// Partial votes are replayed from the durable GM feeds on future scans.
	if !progress {
		return ctx.Err()
	}
	return nil
}

// mergeApprovalLane only receives bounded pages authenticated by Poll.
func (b *ApprovalCollector) mergeApprovalLane(omitted int, pages [4]*protocol.ApprovalPage) error {
	var frontier []byte
	for _, page := range pages {
		if page != nil && len(page.Next) > 0 && (frontier == nil || bytes.Compare(page.Next, frontier) < 0) {
			frontier = page.Next
		}
	}
	// EOF covers the remaining range. Only three EOFs wrap this lane, so late
	// retained approvals are revisited without moving an unavailable lane.
	if frontier == nil {
		b.after[omitted] = nil
		return nil
	}
	// Only this triple contributes. Partial votes never cross a scan range.
	b.pending = make(map[protocol.SpendFactID]*pendingApproval, 3*protocol.ApprovalFeedPageSize)
	for index, page := range pages {
		if page == nil {
			continue
		}
		for _, item := range page.Items {
			fact := item.Approval.Summary.Fact()
			if bytes.Compare(state.Key(239, fact[:]), frontier) > 0 {
				continue
			}
			id := item.Request.Tx.ID()
			exists := false
			if err := b.collector.db.View(func(v state.ReadView) error {
				_, err := v.Get(state.Key(state.KeyCollected, id[:]))
				exists = err == nil
				if errors.Is(err, state.ErrNotFound) {
					return nil
				}
				return err
			}); err != nil {
				return err
			}
			if exists {
				continue
			}
			p := b.pending[fact]
			if p == nil {
				p = &pendingApproval{request: item.Request, summary: item.Approval.Summary}
				b.pending[fact] = p
			}
			vote := item.Approval.Vote
			p.votes[index] = &vote
			cert := protocol.OutputCertificate{Summary: p.summary, QC: protocol.SpendQC{Fact: fact}}
			for _, v := range p.votes {
				if v != nil {
					cert.QC.Votes = append(cert.QC.Votes, *v)
				}
			}
			if len(cert.QC.Votes) < 3 {
				continue
			}
			if err := cert.Verify(b.collector.org); err != nil {
				return err
			}
			payment := protocol.DirectPayment{Tx: p.request.Tx, Certificate: cert, InputCertificates: p.request.InputCertificates}
			if err := b.collector.PersistDirect(payment); err != nil {
				return err
			}
			delete(b.pending, fact)
		}
	}
	b.after[omitted] = bytes.Clone(frontier)
	return nil
}

func (b *ApprovalCollector) Run(ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := b.Poll(ctx); err != nil && ctx.Err() == nil && err != context.DeadlineExceeded {
				return err
			}
		}
	}
}
