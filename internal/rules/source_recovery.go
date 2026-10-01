package rules

import (
	"utxo/internal/state"
	"utxo/protocol"
)

// recoverOutput is called only inside successful source execution. It repays
// the exact reserve which funded this consumed output, without increasing the
// grant or reviving the user's consumed instance. Paid remains a gross counter.
func (e *directEval) recoverOutput(ob *DirectObligation) error {
	if ob.Status != DirectRepaired {
		return ErrConflict
	}
	key := state.Key(keyDirectCoverage, ob.Certificate[:])
	c, found, err := state.Load[directCoverage](e.o, key)
	if err != nil {
		return err
	}
	if !found || c.Summary.Issuer != ob.Issuer {
		return ErrAccounting
	}
	c.Credit.Recovered, err = protocol.Add(c.Credit.Recovered, ob.Amount)
	if err != nil || c.Credit.Recovered > c.Credit.Paid {
		return ErrAccounting
	}
	c.Credit.Revision++
	for _, a := range c.Summary.Admission {
		if a.Key.Kind != protocol.ResourceCAL {
			continue
		}
		if a.Key.Account != ob.Issuer {
			return protocol.ErrAuth
		}
		usageKey := state.Key(state.KeyUsage, a.Key.Encode())
		u, found, err := state.Load[PublicUsage](e.o, usageKey)
		if err != nil {
			return err
		}
		if !found {
			return ErrAccounting
		}
		u.Spent, err = protocol.Sub(u.Spent, ob.Amount)
		if err != nil {
			return err
		}
		if err = state.Put(e.o, usageKey, u); err != nil {
			return err
		}
	}
	if err = changeAmount(e.o, AccountKey(ob.Issuer, protocol.AssetCAL), ob.Amount, false); err != nil {
		return err
	}
	ob.Status = DirectRecovered
	if err = state.Put(e.o, DirectObligationKey(ob.Output), *ob); err != nil {
		return err
	}
	return state.Put(e.o, key, c)
}
