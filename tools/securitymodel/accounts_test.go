package securitymodel

import (
	"fmt"
	"testing"
)

type accountState struct {
	balance           [3]int // two protected accounts and one external funding account
	grant, spent      [2]int
	applied, repaired uint8
}

type accountModel struct {
	backing                       [2]int
	funding                       int
	protectedSource, selfTransfer bool
}

func (m accountModel) initial() accountState {
	s := accountState{grant: [2]int{3, 3}}
	for _, a := range m.backing {
		s.balance[a] += 3
	}
	s.balance[2] = m.funding
	return s
}

func (m accountModel) next(s accountState) (out []edge[accountState]) {
	for g, a := range m.backing {
		bit := uint8(1 << g)
		// One authenticated topup identity per grant, with Previous=3.
		if s.applied&bit == 0 && s.grant[g] == 3 {
			for source := 0; source < 3; source++ {
				if source != 2 && !(m.protectedSource && source != a) && !(m.selfTransfer && source == a) {
					continue
				}
				if s.balance[source] < 1 {
					continue
				}
				n := s
				n.balance[source]--
				n.balance[a]++
				if m.selfTransfer && source == a {
					n.balance[a] = s.balance[a] + 1
				}
				n.grant[g]++
				n.applied |= bit
				out = append(out, edge[accountState]{n, fmt.Sprintf("topup(source%d,g%d)", source, g)})
			}
		}
		if s.repaired&bit == 0 && s.balance[a] > 0 {
			n := s
			n.balance[a]--
			n.spent[g]++
			n.repaired |= bit
			out = append(out, edge[accountState]{n, fmt.Sprintf("pay(g%d)", g)})
		}
	}
	out = append(out, edge[accountState]{s, "replay-or-stale"})
	return out
}

func (m accountModel) check(s accountState) string {
	total := s.spent[0] + s.spent[1]
	for _, v := range s.balance {
		if v < 0 {
			return "negative balance"
		}
		total += v
	}
	if total != 6+m.funding {
		return "account conservation"
	}
	for a := 0; a < 2; a++ {
		exposure := 0
		for g, account := range m.backing {
			if a == account {
				exposure += s.grant[g] - s.spent[g]
			}
		}
		if exposure > s.balance[a] {
			return "shared backing deficit"
		}
	}
	return ""
}

func TestAccountsBounded(t *testing.T) {
	for _, backing := range [][2]int{{0, 1}, {0, 0}} {
		for _, funding := range []int{1, 2} {
			m := accountModel{backing: backing, funding: funding}
			t.Run(fmt.Sprintf("%v/funding%d", backing, funding), func(t *testing.T) { requireExhausted(t, search(m.initial(), m.next, m.check, 1000)) })
		}
	}
}

func TestAccountsMutants(t *testing.T) {
	m := accountModel{backing: [2]int{0, 1}, protectedSource: true}
	requireWitness(t, search(m.initial(), m.next, m.check, 1000), "shared backing deficit")
	m = accountModel{backing: [2]int{0, 1}, selfTransfer: true}
	requireWitness(t, search(m.initial(), m.next, m.check, 1000), "account conservation")
}
