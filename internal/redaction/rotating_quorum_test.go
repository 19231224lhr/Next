//go:build comet_v3

package redaction_test

import (
	"context"
	"errors"
	"testing"

	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

// A single Byzantine signer can rotate the two honest co-signers. This is
// a tightness check of the funded-loss bound, not an economic-profit model.
// Honest approvals and all public accounting use the production entry points.
func TestRotatingQuorumsReachFundedLossBound(t *testing.T) {
	x := newBoundaryLab(t)
	pairs := [][2]int{{0, 1}, {0, 2}, {1, 2}}
	var reserved [3]uint64
	for round, pair := range pairs {
		nonce := uint64(700 + round)
		request := x.tx(t, 0, round, nonce, true, round, nil, 1)
		loser := x.approve(t, 0, request, pair[0], pair[1])
		// Member 3 is Byzantine: it signs without applying its local budget.
		loser.Certificate.QC.Votes = append(loser.Certificate.QC.Votes,
			protocol.SignSpend(loser.Certificate.QC.Fact, 3, x.f[0].Keys[3]))
		if err := loser.Certificate.Verify(x.f[0].Org); err != nil {
			t.Fatal(err)
		}
		for _, signer := range pair {
			reserved[signer] += 100
		}
		winner := x.approve(t, 1, x.tx(t, 1, round, nonce, true, round, nil, 1), 0, 1, 2)
		if winner.Tx.Body.Intent != loser.Tx.Body.Intent {
			t.Fatal("fixture must collide in global Intent")
		}
		x.append(t, boundaryWire(t, winner))
		x.appendOne(t, boundaryWire(t, loser), true)
		x.appendOne(t, nil)
		child := x.approve(t, 1, x.tx(t, 1, 0, uint64(800+round), true, round+3, &loser, 1), 0, 1, 2)
		h := x.append(t, boundaryWire(t, child))
		x.append(t, protocol.ClockTick(x.f[0].Org.Network, x.height+1))
		decision := protocol.CompensationDecision{Network: x.f[0].Org.Network,
			Output: loser.Certificate.Summary.OutputID(0), Height: h, Transaction: 0, Input: 0}
		raw, err := decision.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		x.append(t, raw)
		x.append(t, raw) // Repeated public decision must not spend more principal.
		paid := uint64(round+1) * 100
		balance := boundaryLoad[uint64](t, x.ledger, rules.AccountKey(x.f[0].Org.Org, protocol.AssetCAL))
		usage := boundaryRequire[rules.PublicUsage](t, x.ledger,
			state.Key(state.KeyUsage, x.f[0].Genesis.Grants[0].Key.Encode()))
		if balance != 300-paid || usage.Reserved != 0 || usage.Spent != paid {
			t.Fatalf("round %d balance=%d usage=%+v", round+1, balance, usage)
		}
		for i, debit := range reserved {
			x.quota(t, i, 200-debit, debit)
		}
		if got := x.money(t); got != x.baseline {
			t.Fatalf("CAL/FUEL conservation: %v != %v", got, x.baseline)
		}
		t.Logf("round=%d reserve=%d net_paid=%d honest_reserved=%v", round+1, balance, paid, reserved)
	}
	next := x.tx(t, 0, 3, 999, true, 9, nil, 1)
	for i := 0; i < 3; i++ {
		if _, err := x.m[0][i].ApproveDirect(context.Background(), next); !errors.Is(err, rules.ErrLimited) {
			t.Fatalf("exhausted honest member %d returned %v", i, err)
		}
	}
}
