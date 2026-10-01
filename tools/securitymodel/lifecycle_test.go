package securitymodel

import (
	"fmt"
	"math/bits"
	"testing"
)

const (
	unused uint8 = iota
	open
	fulfilled
	repaired
	recovered
)

type local struct {
	cursor, seenPaid, applied, available, reserved, grant int
	approvedAt                                            int
	settled                                               bool
}

// One hidden, fully signed certificate has two unit outputs. Its original
// final input is worth two. A log event is atomic; each honest member follows
// that same log one event at a time, at any relative speed.
type lifeState struct {
	approved                                                     uint8
	outputs                                                      [2]uint8
	promises, created, consumed                                  [2]uint8
	childCoins                                                   int
	settled, registered, topped                                  bool
	paid, remaining, spent, grant, account, external, coins, gap int
	recovered                                                    int
	log                                                          [6]uint8 // 1,2=open; 3,4=repair; 5=source; 6=topup
	length                                                       int
	members                                                      [3]local
}

func (m lifeModel) initial() lifeState {
	s := lifeState{approved: m.approved, grant: 3, account: 3, external: 3, coins: 2}
	for i := range s.members {
		s.members[i] = local{available: 2, grant: 3}
		if m.approved&(1<<i) != 0 {
			s.members[i].available = 0
			s.members[i].reserved = 2
		}
	}
	return s
}

type lifeModel struct {
	approved, signed         uint8
	returnPaid, earlyRelease bool
	allowLate, allSigners    bool
	recoverSource            bool
}

func (m lifeModel) next(s lifeState) (out []edge[lifeState]) {
	add := func(n lifeState, event uint8, name string) {
		if event != 0 {
			n.log[n.length] = event
			n.length++
		}
		out = append(out, edge[lifeState]{n, name})
	}
	for i, status := range s.outputs {
		if status == unused && !s.settled {
			n := s
			if !n.registered {
				n.registered = true
				n.remaining = 2
				n.promises = [2]uint8{open, open}
			}
			n.outputs[i] = open
			n.consumed[i] |= 1
			n.childCoins++
			n.coins++
			n.gap++
			add(n, uint8(1+i), fmt.Sprintf("consume-missing(o%d)", i))
		}
		if status == open {
			n := s
			n.outputs[i] = repaired
			n.promises[i] = repaired
			n.paid++
			n.spent++
			n.remaining--
			n.account--
			n.gap--
			add(n, uint8(3+i), fmt.Sprintf("repair(o%d)", i))
		}
		for instance := 0; instance < 2; instance++ {
			bit := uint8(1 << instance)
			if s.created[i]&bit != 0 && s.consumed[i]&bit == 0 {
				n := s
				n.consumed[i] |= bit
				n.childCoins++
				// One final input becomes one final child output: coins unchanged.
				// Its unrelated follower event is a stutter for this certificate.
				add(n, 0, fmt.Sprintf("consume-final(o%d,i%d)", i, instance))
			}
		}
	}
	if !s.settled {
		n := s
		n.settled = true
		n.coins -= 2
		for i, status := range s.outputs {
			if n.promises[i] == open {
				n.promises[i] = fulfilled
			}
			n.created[i] = 1
			switch status {
			case open:
				n.outputs[i] = fulfilled
				n.gap--
			case repaired:
				if m.recoverSource {
					n.created[i] = 0
					n.outputs[i] = recovered
					n.account++
					n.recovered++
					n.spent--
				} else {
					n.created[i] = 2
					n.coins++
				}
			case unused:
				n.coins++
			}
		}
		n.remaining = 0
		add(n, 5, "source-arrives")
	}
	if !s.topped {
		n := s
		n.topped = true
		n.external -= 3
		n.account += 3
		n.grant += 3
		add(n, 6, "topup-external")
	}
	for i, l := range s.members {
		if m.allowLate && s.approved&(1<<i) == 0 && l.available >= 2 {
			observed := false
			for j := 0; j < l.cursor; j++ {
				observed = observed || s.log[j] == 5
			}
			if !observed {
				n := s
				n.approved |= 1 << i
				n.members[i].approvedAt = l.cursor
				n.members[i].available -= 2
				n.members[i].reserved += 2
				add(n, 0, fmt.Sprintf("late-approve(m%d,prefix%d)", i, l.cursor))
			}
		}
		if l.cursor >= s.length {
			continue
		}
		n := s
		p := &n.members[i]
		approved := s.approved&(1<<i) != 0
		switch s.log[p.cursor] {
		case 3, 4:
			if approved {
				p.seenPaid++
			}
		case 5:
			if approved {
				p.settled = true
			}
		case 6:
			p.available += 2*(p.grant+3)/3 - 2*p.grant/3
			p.grant += 3
		}
		p.cursor++
		target := 0
		if p.settled || m.earlyRelease && p.seenPaid > 0 {
			target = 2 - p.seenPaid
		}
		if p.settled && (m.returnPaid || m.recoverSource) {
			target = 2
		}
		delta := target - p.applied
		p.available += delta
		p.reserved -= delta
		p.applied = target
		add(n, 0, fmt.Sprintf("follow(m%d,event%d)", i, p.cursor))
	}
	// Repeated submission/repair/follow of already applied identities is a
	// stuttering step. Detailed identity checks are exercised by implementation tests.
	add(s, 0, "duplicate")
	return out
}

func (m lifeModel) check(s lifeState) string {
	if bits.OnesCount8(m.signed) < 3 || (m.signed&7)&^m.approved != 0 {
		return "invalid initial QC"
	}
	gap, paid, remaining, coins := 0, 0, 0, s.childCoins
	if !s.settled {
		coins += 2
	}
	for i, status := range s.outputs {
		if status == open {
			gap++
		}
		if status == repaired || status == recovered {
			paid++
		}
		creation := uint8(0)
		if s.settled {
			creation = 1
			if status == repaired {
				creation = 2
			}
			if status == recovered {
				creation = 0
			}
		}
		if s.created[i] != creation {
			return "late instance"
		}
		coins += bits.OnesCount8(s.created[i] &^ s.consumed[i])
		promise := uint8(unused)
		if s.registered {
			promise = open
			if s.settled {
				promise = fulfilled
			}
			if status == repaired || status == recovered {
				promise = repaired
			}
		}
		if s.promises[i] != promise {
			return "promise mismatch"
		}
		if promise == open {
			remaining++
		}
		if s.settled && status == open {
			return "settled open obligation"
		}
	}
	if s.remaining != remaining || s.coins != coins {
		return "asset projection"
	}
	if s.gap != gap || s.paid != paid || s.spent != paid-s.recovered || s.recovered > paid {
		return "obligation accounting"
	}
	if s.coins+s.account+s.external-s.gap != 8 {
		return "CAL conservation"
	}
	if s.account < 0 || s.external < 0 || s.grant-s.spent > s.account {
		return "backing"
	}
	w := 2
	if s.settled {
		w = s.paid - s.recovered
	}
	if s.remaining < s.gap || s.remaining > w-(s.paid-s.recovered) {
		return "public versus hidden risk"
	}
	witnessResidual := 0
	for i, l := range s.members {
		cap := 0
		if s.approved&(1<<i) != 0 {
			cap = 2
		}
		if l.applied < 0 || l.applied > cap || l.reserved != cap-l.applied || l.available+l.reserved != 2*l.grant/3 {
			return "local accounting"
		}
		if (m.signed&(1<<i) != 0 || m.allSigners && cap > 0) && l.reserved < w {
			return "residual below risk"
		}
		if m.signed&(1<<i) != 0 {
			witnessResidual += l.reserved
		}
		if l.grant > s.grant || l.seenPaid > s.paid || l.settled && !s.settled {
			return "future observation"
		}
		// Derive the local snapshot independently from its exact public prefix.
		p, b, settled := 0, 3, false
		for j := 0; j < l.cursor; j++ {
			switch s.log[j] {
			case 3, 4:
				if j >= l.approvedAt {
					p++
				}
			case 5:
				settled = true
			case 6:
				b += 3
			}
		}
		if cap == 0 {
			p = 0
			settled = false
		}
		if l.seenPaid != p || l.grant != b || l.settled != settled {
			return "prefix mismatch"
		}
	}
	if 2*w > witnessResidual || w > s.grant {
		return "witness coverage"
	}
	return ""
}

// The pre-recovery model remains above as an explicit old-rule baseline.
// Under source subrogation every complete arrival restores the same principal
// allocation, even when followers lag and an extra signer joins late.
func TestLifecycleSourceRecovery(t *testing.T) {
	for _, late := range []bool{false, true} {
		m := lifeModel{approved: 3, signed: 11, allowLate: late, allSigners: true, recoverSource: true}
		check := func(s lifeState) string {
			if err := m.check(s); err != "" {
				return err
			}
			if s.settled && (s.coins != 2 || s.spent != 0 || s.account != s.grant) {
				return "delay creates principal profit"
			}
			return ""
		}
		t.Run(fmt.Sprintf("new-rule/late%v", late), func(t *testing.T) {
			requireExhausted(t, search(m.initial(), m.next, check, 2000000))
		})
		m.recoverSource = false
		t.Run(fmt.Sprintf("old-rule-counterexample/late%v", late), func(t *testing.T) {
			requireWitness(t, search(m.initial(), m.next, check, 2000000), "delay creates principal profit")
		})
	}
}

// The original model fixes all approvals before the public history. This
// extension permits an extra member to approve after following a repair.
// The old universal-signers claim is deliberately checked as a false property;
// the early witness QC and accounting invariants must still exhaust safely.
func TestLifecycleLateApprovalWitness(t *testing.T) {
	m := lifeModel{approved: 3, signed: 11, allowLate: true}
	t.Run("early-witness", func(t *testing.T) {
		requireExhausted(t, search(m.initial(), m.next, m.check, 2000000))
	})
	t.Run("all-signers-is-too-strong", func(t *testing.T) {
		m.allSigners = true
		requireWitness(t, search(m.initial(), m.next, m.check, 2000000), "residual below risk")
	})
}

func TestLifecycleBounded(t *testing.T) {
	for _, m := range []lifeModel{{approved: 3, signed: 11}, {approved: 7, signed: 7}, {approved: 7, signed: 11}} {
		t.Run(fmt.Sprintf("approved%04b/signed%04b", m.approved, m.signed), func(t *testing.T) {
			requireExhausted(t, search(m.initial(), m.next, m.check, 500000))
		})
	}
}

func TestLifecycleMutants(t *testing.T) {
	for _, m := range []lifeModel{{returnPaid: true}, {earlyRelease: true}} {
		m.approved = 3
		m.signed = 11
		requireWitness(t, search(m.initial(), m.next, m.check, 500000), "residual below risk")
	}
}
