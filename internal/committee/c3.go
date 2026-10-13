package committee

import (
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

type C3PoolSnapshot struct {
	CumulativeFees, Released, PoolRewards, PoolBurned                     uint64
	Grant                                                                 state.Grant
	OriginalFunding, Funding, Free, Held, Spent, Revenue, Rewards, Burned uint64
}
type C3Snapshot struct {
	Record rules.C3Record
	Pool   C3PoolSnapshot
}

func (e *Engine) c3Pool(v state.ReadView, key protocol.ResourceKey) (s C3PoolSnapshot, err error) {
	if e.direct == nil || !e.direct.IsC3Pool(key.Account) || key.Kind != protocol.ResourceFUEL {
		return s, protocol.ErrAuth
	}
	var issuer rules.C3Issuer
	found := false
	for _, i := range e.direct.C3.Issuers {
		if i.Funding.Key == key {
			issuer = i
			found = true
			break
		}
	}
	if !found {
		return s, protocol.ErrAuth
	}
	s.Grant, found, err = state.Load[state.Grant](v, state.Key(state.KeyGrant, key.Encode()))
	if err != nil {
		return s, err
	}
	if !found {
		return s, state.ErrNotFound
	}
	s.Funding = s.Grant.Amount
	for _, g := range e.cfg.Genesis.Grants {
		if g.Key == key {
			s.OriginalFunding = g.Amount
		}
	}
	u, _, err := state.Load[rules.PublicUsage](v, state.Key(state.KeyUsage, key.Encode()))
	if err != nil {
		return s, err
	}
	s.Held, s.Spent = u.Reserved, u.Spent
	totals, _, err := state.Load[rules.C3PoolTotals](v, rules.C3PoolTotalsKey(key))
	if err != nil {
		return s, err
	}
	s.CumulativeFees, s.Released, s.PoolRewards, s.PoolBurned = totals.CumulativeFees, totals.Released, totals.Rewards, totals.Burned
	for _, x := range []struct {
		k   []byte
		dst *uint64
	}{{rules.AccountKey(key.Account, protocol.AssetFUEL), &s.Free}, {rules.AccountKey(issuer.Revenue, protocol.AssetFUEL), &s.Revenue}, {state.Key(state.KeyReward, issuer.Beneficiary[:]), &s.Rewards}, {state.Key(state.KeyBurned), &s.Burned}} {
		*x.dst, _, err = state.Load[uint64](v, x.k)
		if err != nil {
			return s, err
		}
	}
	return s, nil
}

// C3Pool and C3Fee read a single consistent store snapshot and never mutate it.
func (e *Engine) C3Pool(key protocol.ResourceKey) (s C3PoolSnapshot, err error) {
	err = e.db.View(func(v state.ReadView) error { var e2 error; s, e2 = e.c3Pool(v, key); return e2 })
	return
}
func (e *Engine) C3Fee(fact protocol.SpendFactID) (s C3Snapshot, err error) {
	err = e.db.View(func(v state.ReadView) error {
		var found bool
		var e2 error
		s.Record, found, e2 = state.Load[rules.C3Record](v, rules.C3EscrowKey(fact))
		if e2 != nil {
			return e2
		}
		if !found {
			return state.ErrNotFound
		}
		s.Pool, e2 = e.c3Pool(v, s.Record.Escrow.Commitment.Funding().Key)
		return e2
	})
	return
}
