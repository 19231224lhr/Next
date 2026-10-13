package gateway

import (
	"context"
	"testing"

	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestCollectDirectInvalidVoteUsesValidFourth(t *testing.T) {
	c, _, payments := directRelayFixture(t, 1)
	p := payments[0]
	f := testkit.NewFixture("direct-relay", "org", 1)
	for i := range c.members {
		vote := protocol.SignSpend(p.Certificate.Summary.Fact(), uint16(i), f.Keys[i])
		if i == 0 {
			vote.Signature[0] ^= 1
		}
		c.members[i] = encodedDirectClient{call: func(context.Context, []byte) (protocol.DirectApproval, error) {
			return protocol.DirectApproval{Summary: p.Certificate.Summary, Vote: vote}, nil
		}}
	}
	cert, err := c.CollectDirect(context.Background(), protocol.DirectRequest{Tx: p.Tx})
	if err != nil || cert.Verify(c.org) != nil {
		t.Fatalf("valid fourth did not recover quorum: %v", err)
	}
	for _, vote := range cert.QC.Votes {
		if vote.Member == 0 {
			t.Fatal("invalid vote included")
		}
	}
}

func TestCollectDirectRejectsSignedMismatchedSummary(t *testing.T) {
	mutations := map[string]func(*protocol.OutputSummary){
		"transaction":       func(s *protocol.OutputSummary) { s.Tx[0] ^= 1 },
		"output":            func(s *protocol.OutputSummary) { s.Outputs[0].Digest[0] ^= 1 },
		"grant":             func(s *protocol.OutputSummary) { s.Grants[0][0] ^= 1 },
		"rules":             func(s *protocol.OutputSummary) { s.Rules.Fee[0] ^= 1 },
		"invalid_admission": func(s *protocol.OutputSummary) { s.Admission[0].Cap = 0 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			c, _, payments := directRelayFixture(t, 1)
			p := payments[0]
			s := p.Certificate.Summary
			mutate(&s)
			f := testkit.NewFixture("direct-relay", "org", 1)
			for i := range c.members {
				vote := protocol.SignSpend(s.Fact(), uint16(i), f.Keys[i])
				c.members[i] = encodedDirectClient{call: func(context.Context, []byte) (protocol.DirectApproval, error) {
					return protocol.DirectApproval{Summary: s, Vote: vote}, nil
				}}
			}
			if _, err := c.CollectDirect(context.Background(), protocol.DirectRequest{Tx: p.Tx}); err == nil {
				t.Fatal("signed mismatched summary accepted")
			}
		})
	}
}

func TestCollectDirectKeepsDifferentAdmissionsSeparate(t *testing.T) {
	c, _, payments := directRelayFixture(t, 1)
	p := payments[0]
	f := testkit.NewFixture("direct-relay", "org", 1)
	for i := range c.members {
		s := p.Certificate.Summary
		s.Admission = append(protocol.AdmissionVector(nil), s.Admission...)
		if i >= 2 {
			// A non-CAL capacity can differ while both summaries remain valid.
			for j := range s.Admission {
				if s.Admission[j].Key.Kind == protocol.ResourceFUEL {
					s.Admission[j].Cap++
					break
				}
			}
		}
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
		vote := protocol.SignSpend(s.Fact(), uint16(i), f.Keys[i])
		c.members[i] = encodedDirectClient{call: func(context.Context, []byte) (protocol.DirectApproval, error) {
			return protocol.DirectApproval{Summary: s, Vote: vote}, nil
		}}
	}
	if _, err := c.CollectDirect(context.Background(), protocol.DirectRequest{Tx: p.Tx}); err == nil {
		t.Fatal("different admissions combined into quorum")
	}
}
