package securitymodel

import (
	"fmt"
	"math/bits"
	"testing"
)

// Members 0..2 are honest; member 3 can sign either fact without approval.
// Approval and signing are distinct events. Publication is not required for QC.
type voteState struct {
	approved, voted [4]uint8
	published       uint8
}

type voteModel struct {
	budget, amount                     int
	conflict, forgetLock, bypassBudget bool
}

func (s voteState) qc(f int) bool {
	n := 0
	for _, v := range s.voted {
		if v&(1<<f) != 0 {
			n++
		}
	}
	return n >= 3
}

func (m voteModel) next(s voteState) (out []edge[voteState]) {
	for f := 0; f < 2; f++ {
		bit := uint8(1 << f)
		for member := 0; member < 4; member++ {
			if member < 3 && s.approved[member]&bit == 0 {
				locked := m.conflict && s.approved[member] != 0 && !m.forgetLock
				used := bits.OnesCount8(s.approved[member]) * m.amount
				if !locked && (used+m.amount <= 2*m.budget/3 || m.bypassBudget) {
					n := s
					n.approved[member] |= bit
					out = append(out, edge[voteState]{n, fmt.Sprintf("approve(m%d,f%d)", member, f)})
				}
			}
			if s.voted[member]&bit == 0 && (member == 3 || s.approved[member]&bit != 0) {
				n := s
				n.voted[member] |= bit
				out = append(out, edge[voteState]{n, fmt.Sprintf("vote(m%d,f%d)", member, f)})
			}
		}
		if s.qc(f) && s.published&bit == 0 {
			n := s
			n.published |= bit
			out = append(out, edge[voteState]{n, fmt.Sprintf("publish(f%d)", f)})
		}
	}
	return out
}

func (m voteModel) check(s voteState) string {
	// Check end properties independently of the guards in next.
	if m.conflict && s.qc(0) && s.qc(1) {
		return "conflicting QC"
	}
	risk := 0
	for f := 0; f < 2; f++ {
		if s.qc(f) {
			risk += m.amount
		}
	}
	if risk > m.budget {
		return "uncovered QC"
	}
	for member := 0; member < 3; member++ {
		if s.voted[member] & ^s.approved[member] != 0 {
			return "vote without approval"
		}
	}
	return ""
}

func TestVotesBounded(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		for _, budget := range []int{2, 3, 4, 6} {
			m := voteModel{budget: budget, amount: 2, conflict: conflict}
			t.Run(fmt.Sprintf("B%d/conflict%v", budget, conflict), func(t *testing.T) {
				r := search(voteState{}, m.next, m.check, 100000)
				requireExhausted(t, r)
			})
		}
	}
}

func TestVotesMutants(t *testing.T) {
	m := voteModel{budget: 6, amount: 2, conflict: true, forgetLock: true}
	requireWitness(t, search(voteState{}, m.next, m.check, 100000), "conflicting QC")
	m = voteModel{budget: 3, amount: 2, bypassBudget: true}
	requireWitness(t, search(voteState{}, m.next, m.check, 100000), "uncovered QC")
}

func TestHiddenQCAndPartialApprovals(t *testing.T) {
	s := voteState{approved: [4]uint8{1, 1, 2, 0}, voted: [4]uint8{1, 1, 2, 1}}
	if !s.qc(0) || s.qc(1) || s.published != 0 {
		t.Fatal("hidden QC and partial approval conflated")
	}
	m := voteModel{budget: 3, amount: 2, conflict: true}
	if reason := m.check(s); reason != "" {
		t.Fatal(reason)
	}
	// All honest budgets are occupied, but only fact 0 has a certificate.
	for _, e := range m.next(s) {
		if e.next.approved != s.approved {
			t.Fatal("partial approvals were silently released")
		}
	}
}
