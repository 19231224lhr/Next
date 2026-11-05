package rules

import (
	"bytes"
	"sort"
	"utxo/internal/state"
	"utxo/protocol"
)

// C3EpochState retains payer attribution across epoch boundaries, including
// integer dust and periods with no other eligible capital provider.
type C3EpochState struct {
	LastHeight, Epoch uint64
	Weights           []uint64
	Pool              []C3PoolPart
	Credits           []protocol.FuelCredit
}
type C3PoolPart struct {
	Payer  protocol.Hash
	Amount uint64
}

func C3EpochKey() []byte { return state.Key(174) }
func C3EpochHistoryKey(epoch uint64) []byte {
	e := new(protocol.Encoder)
	e.U64(epoch)
	return state.Key(176, e.Data())
}
func (p DirectPolicy) FinalC3() bool {
	if p.C3 != nil {
		for _, x := range p.C3.Policies {
			if x.Version == protocol.C3FinalVersion {
				return true
			}
		}
	}
	return false
}
func addC3Pool(o *state.Overlay, payer protocol.Hash, amount uint64) error {
	if amount == 0 {
		return nil
	}
	s, _, e := state.Load[C3EpochState](o, C3EpochKey())
	if e != nil {
		return e
	}
	for i := range s.Pool {
		if s.Pool[i].Payer == payer {
			s.Pool[i].Amount, e = protocol.Add(s.Pool[i].Amount, amount)
			if e != nil {
				return e
			}
			return state.Put(o, C3EpochKey(), s)
		}
	}
	s.Pool = append(s.Pool, C3PoolPart{payer, amount})
	sort.Slice(s.Pool, func(i, j int) bool { return bytes.Compare(s.Pool[i].Payer[:], s.Pool[j].Payer[:]) < 0 })
	return state.Put(o, C3EpochKey(), s)
}
func BeginC3Epoch(v state.ReadView, p DirectPolicy, height uint64) (state.Transition, error) {
	if !p.FinalC3() {
		return state.Transition{}, nil
	}
	if height == 0 || p.C3.EpochBlocks == 0 {
		return state.Transition{}, protocol.ErrRule
	}
	o := state.NewOverlay(v)
	s, _, e := state.Load[C3EpochState](o, C3EpochKey())
	if e != nil {
		return state.Transition{}, e
	}
	if height == s.LastHeight {
		return state.Transition{}, nil
	}
	if height != s.LastHeight+1 {
		return state.Transition{}, protocol.ErrRule
	}
	if len(s.Weights) == 0 {
		s.Weights = make([]uint64, len(p.C3.Issuers))
	}
	if len(s.Weights) != len(p.C3.Issuers) {
		return state.Transition{}, protocol.ErrRule
	}
	epoch := (height - 1) / p.C3.EpochBlocks
	if epoch != s.Epoch {
		for i := range s.Pool {
			part := &s.Pool[i]
			var denom uint64
			for j, issuer := range p.C3.Issuers {
				if p.Organizations[issuer.Config].Org != part.Payer {
					denom, e = protocol.Add(denom, s.Weights[j])
					if e != nil {
						return state.Transition{}, e
					}
				}
			}
			if denom == 0 {
				continue
			}
			original := part.Amount
			for j, issuer := range p.C3.Issuers {
				if p.Organizations[issuer.Config].Org == part.Payer {
					continue
				}
				amount, err := protocol.MulDiv(original, s.Weights[j], denom)
				if err != nil {
					return state.Transition{}, err
				}
				if amount == 0 {
					continue
				}
				part.Amount -= amount
				key := state.Key(state.KeyGrant, issuer.Funding.Key.Encode())
				g, found, err := state.Load[state.Grant](o, key)
				if err != nil {
					return state.Transition{}, err
				}
				if !found || g.ID != issuer.Funding.Grant {
					return state.Transition{}, ErrMissing
				}
				g.Amount, err = protocol.Add(g.Amount, amount)
				if err != nil {
					return state.Transition{}, err
				}
				if err = state.Put(o, key, g); err != nil {
					return state.Transition{}, err
				}
				if err = changeAmount(o, AccountKey(issuer.Funding.Key.Account, protocol.AssetFUEL), amount, false); err != nil {
					return state.Transition{}, err
				}
				foundCredit := false
				for k := range s.Credits {
					if s.Credits[k].Key == g.Key {
						s.Credits[k].Cumulative, err = protocol.Add(s.Credits[k].Cumulative, amount)
						if err != nil {
							return state.Transition{}, err
						}
						foundCredit = true
					}
				}
				if !foundCredit {
					s.Credits = append(s.Credits, protocol.FuelCredit{Key: g.Key, Grant: g.ID, Organization: g.Organization, Cumulative: amount})
				}
			}
		}
		if e = state.Put(o, C3EpochHistoryKey(s.Epoch), s); e != nil {
			return state.Transition{}, e
		}
		s.Epoch = epoch
		for i := range s.Weights {
			s.Weights[i] = 0
		}
	}
	for j, issuer := range p.C3.Issuers {
		balance, _, err := state.Load[uint64](o, AccountKey(p.Organizations[issuer.Config].Org, protocol.AssetCAL))
		if err != nil {
			return state.Transition{}, err
		}
		s.Weights[j], err = protocol.Add(s.Weights[j], balance)
		if err != nil {
			return state.Transition{}, err
		}
	}
	s.LastHeight = height
	sort.Slice(s.Credits, func(i, j int) bool { return bytes.Compare(s.Credits[i].Key.Encode(), s.Credits[j].Key.Encode()) < 0 })
	if e = state.Put(o, C3EpochKey(), s); e != nil {
		return state.Transition{}, e
	}
	return state.Transition{Changes: o.Changes()}, nil
}

func ApplyC3Credit(o *state.Overlay, c protocol.FuelCredit, org protocol.Hash, workers uint32) error {
	if c.Organization != org {
		return nil
	}
	key := state.Key(175, c.Key.Encode())
	old, _, e := state.Load[uint64](o, key)
	if e != nil {
		return e
	}
	if c.Cumulative <= old {
		return nil
	}
	g, found, e := state.Load[state.Grant](o, state.Key(state.KeyGrant, c.Key.Encode()))
	if e != nil {
		return e
	}
	if !found || g.ID != c.Grant {
		return protocol.ErrAuth
	}
	if e = ApplyReserveIncrease(o, protocol.ReserveIncrease{Organization: org, Key: c.Key, Grant: c.Grant, Subject: g.Subject, Previous: g.Amount, Amount: c.Cumulative - old}, org, workers); e != nil {
		return e
	}
	return state.Put(o, key, c.Cumulative)
}
