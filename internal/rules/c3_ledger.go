package rules

import (
	"utxo/internal/state"
	"utxo/protocol"
)

// C3Record exists independently of source execution. QuoteCollected records
// only a successfully executed source; cumulative fees never count as funding.
type C3Record struct {
	Escrow         C3Escrow
	QuoteCollected bool
}
type C3PoolTotals struct{ CumulativeFees, Released, Rewards, Burned, Reflow uint64 }

func C3PoolTotalsKey(key protocol.ResourceKey) []byte { return state.Key(173, key.Encode()) }

func C3EscrowKey(f protocol.SpendFactID) []byte { return state.Key(170, f[:]) }
func (e *directEval) c3Attach(s protocol.OutputSummary) error {
	if err := ValidateC3Summary(s, e.policy); err != nil {
		return err
	}
	if !s.C3.Enabled() {
		return nil
	}
	fact := s.Fact()
	key := C3EscrowKey(fact)
	old, found, err := state.Load[C3Record](e.o, key)
	if err != nil {
		return err
	}
	if found {
		return old.Escrow.ValidateCommitment(s.C3)
	}
	var g state.Grant
	for i, a := range s.Admission {
		if a.Key == s.C3.Funding().Key {
			g, err = e.grant(s, i)
			if err != nil {
				return err
			}
		}
	}
	if g.ID != s.C3.Grant {
		return protocol.ErrAuth
	}
	if err = updateUsage(e.o, g.Key, s.C3.Budget, 0, g.Amount, true); err != nil {
		return err
	}
	if err = changeAmount(e.o, AccountKey(s.C3.Pool, protocol.AssetFUEL), s.C3.Budget, true); err != nil {
		return err
	}
	old.Escrow, err = NewC3Escrow(s.Network, s.Config, fact, s.C3)
	if err != nil {
		return err
	}
	if err = state.Put(e.o, key, old); err != nil {
		return err
	}
	return e.c3Stage(s, C3Attach, C3WholeTransaction)
}
func (e *directEval) c3Stage(s protocol.OutputSummary, stage C3Stage, index uint32) error {
	if !s.C3.Enabled() {
		return nil
	}
	fact := s.Fact()
	key := C3EscrowKey(fact)
	rec, found, err := state.Load[C3Record](e.o, key)
	if err != nil {
		return err
	}
	if !found {
		return ErrMissing
	}
	old := rec.Escrow
	next, err := old.Apply(s.C3, stage, index)
	if err != nil {
		return err
	}
	reflow := next.Reflow - old.Reflow
	reward, burn, release := next.Rewards-old.Rewards, next.Burned-old.Burned, next.Released-old.Released
	if s.C3.Final() {
		exec := uint64(0)
		if !old.has(C3Execute) && next.has(C3Execute) {
			plan, _ := s.C3.Plan()
			exec = plan.ExecuteReward
		}
		if err = e.c3Reward(reward-exec, exec); err != nil {
			return err
		}
		if err = addC3Pool(e.o, s.C3.Issuer, reflow); err != nil {
			return err
		}
	} else if err = changeAmount(e.o, state.Key(state.KeyReward, s.C3.Beneficiary[:]), reward, false); err != nil {
		return err
	}
	if err = changeAmount(e.o, state.Key(state.KeyBurned), burn, false); err != nil {
		return err
	}
	if err = changeAmount(e.o, AccountKey(s.C3.Pool, protocol.AssetFUEL), release, false); err != nil {
		return err
	}
	g, ok, err := state.Load[state.Grant](e.o, state.Key(state.KeyGrant, s.C3.Funding().Key.Encode()))
	if err != nil {
		return err
	}
	if !ok || g.ID != s.C3.Grant {
		return protocol.ErrAuth
	}
	if err = updateUsage(e.o, g.Key, reward+burn+reflow+release, reward+burn+reflow, g.Amount, false); err != nil {
		return err
	}
	rec.Escrow = next
	totals, _, err := state.Load[C3PoolTotals](e.o, C3PoolTotalsKey(g.Key))
	if err != nil {
		return err
	}
	for _, x := range []struct {
		dst    *uint64
		amount uint64
	}{{&totals.Released, release}, {&totals.Rewards, reward}, {&totals.Burned, burn}, {&totals.Reflow, reflow}} {
		*x.dst, err = protocol.Add(*x.dst, x.amount)
		if err != nil {
			return err
		}
	}
	if stage == C3Execute {
		if !rec.QuoteCollected {
			totals.CumulativeFees, err = protocol.Add(totals.CumulativeFees, s.C3.Quote)
			if err != nil {
				return err
			}
		}
		rec.QuoteCollected = true
	}
	if err = state.Put(e.o, C3PoolTotalsKey(g.Key), totals); err != nil {
		return err
	}
	return state.Put(e.o, key, rec)
}
