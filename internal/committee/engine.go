package committee

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type GenesisAccount struct {
	Owner   protocol.Hash
	Asset   protocol.Asset
	Balance uint64
}
type EngineConfig struct {
	Network       protocol.Hash
	Organizations []protocol.OrgConfig
	Schedule      rules.Schedule
	Genesis       state.Genesis
	Accounts      []GenesisAccount
	Direct        *rules.DirectSettings `json:",omitempty"`
}
type Engine struct {
	cache         verificationCache[rules.VerifiedCertificate]
	directCache   verificationCache[rules.VerifiedDirectPayment]
	cfg           EngineConfig
	db            store.Store
	orgs          map[protocol.Hash]protocol.OrgConfig
	protectedCAL  map[string]struct{}
	genesis       protocol.Hash
	direct        *rules.DirectPolicy
	repairCheck   CheckFunc
	repairExecute TimedExecuteFunc
}

func NewEngine(c EngineConfig, db store.Store) (*Engine, error) {
	if c.Network == (protocol.Hash{}) || c.Genesis.Network != c.Network || c.Schedule.Validate() != nil || db == nil {
		return nil, protocol.ErrRule
	}
	// Snapshot configuration; no caller may alter authorization after opening.
	b, e := json.Marshal(c)
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return nil, e
	}
	engine := &Engine{cfg: c, db: db, orgs: make(map[protocol.Hash]protocol.OrgConfig), genesis: protocol.Digest("PUBLIC_GENESIS_V2", b)}
	engine.directCache.disabled = os.Getenv("UTXO_EXPERIMENT_DISABLE_DIRECT_CACHE") == "1"
	if c.Direct != nil {
		p, err := c.Direct.Policy(c.Schedule, c.Organizations)
		if err != nil {
			return nil, err
		}
		engine.direct = &p
		engine.genesis = protocol.Digest("PUBLIC_GENESIS_V3", b)
	}
	for _, org := range c.Organizations {
		if org.Validate() != nil || org.Network != c.Network {
			return nil, protocol.ErrAuth
		}
		if _, ok := engine.orgs[org.Hash()]; ok {
			return nil, protocol.ErrRule
		}
		engine.orgs[org.Hash()] = org
	}
	// Account roles are static, just like organizations and grant identities.
	// Validate before the stored-genesis shortcut so reopening cannot bypass it.
	engine.protectedCAL = make(map[string]struct{})
	for _, org := range c.Organizations {
		engine.protectedCAL[string(rules.AccountKey(org.Org, protocol.AssetCAL))] = struct{}{}
	}
	for _, g := range c.Genesis.Grants {
		if g.Key.Kind == protocol.ResourceCAL {
			engine.protectedCAL[string(rules.AccountKey(g.Key.Account, protocol.AssetCAL))] = struct{}{}
		}
	}
	for _, g := range c.Genesis.Grants {
		if g.Key.Kind == protocol.ResourceCAL {
			source := rules.AccountKey(protocol.ReserveFundingAccount(c.Network, g.Subject), protocol.AssetCAL)
			if _, protected := engine.protectedCAL[string(source)]; protected {
				return nil, protocol.ErrRule
			}
		}
	}
	e = db.Update(func(v state.ReadView) ([]state.Change, error) {
		key := state.Key(state.KeyGenesis)
		old, found, e := state.Load[protocol.Hash](v, key)
		if e != nil {
			return nil, e
		}
		if found {
			if old != engine.genesis {
				return nil, store.ErrIdentity
			}
			return nil, nil
		}
		o := state.NewOverlay(v)
		for _, a := range c.Accounts {
			if a.Owner == (protocol.Hash{}) || (a.Asset != protocol.AssetFUEL && (c.Direct == nil || a.Asset != protocol.AssetCAL)) || a.Balance == 0 {
				return nil, protocol.ErrRule
			}
			k := rules.AccountKey(a.Owner, a.Asset)
			if _, found, e := state.Load[uint64](o, k); e != nil {
				return nil, e
			} else if found {
				return nil, protocol.ErrRule
			}
			if e = state.Put(o, k, a.Balance); e != nil {
				return nil, e
			}
		}
		for _, out := range c.Genesis.Outputs {
			if out.ID == (protocol.OutputID{}) || out.Fact == (protocol.Hash{}) || out.Output.Amount == 0 || out.Output.Recipient.Verify(c.Network) != nil {
				return nil, protocol.ErrRule
			}
			k := state.Key(state.KeyCreation, out.ID[:])
			if c.Direct != nil {
				k = rules.DirectCreationKey(out.ID, 0)
			}
			if _, found, e := state.Load[state.Creation](o, k); e != nil {
				return nil, e
			} else if found {
				return nil, protocol.ErrRule
			}
			if e = state.Put(o, k, state.Creation{Output: out.Output, Fact: out.Fact, Final: true}); e != nil {
				return nil, e
			}
		}
		backing := make(map[string]uint64)
		for _, g := range c.Genesis.Grants {
			if _, ok := engine.orgs[g.Organization]; !ok {
				return nil, protocol.ErrAuth
			}
			minimum := protocol.ResourceFUEL
			if c.Direct != nil {
				minimum = protocol.ResourceCAL
			}
			if g.ID == (protocol.Hash{}) || g.Key.Version == 0 || g.Amount == 0 || g.Key.Kind < minimum || g.Key.Kind > protocol.ResourcePolicy {
				return nil, protocol.ErrRule
			}
			k := state.Key(state.KeyGrant, g.Key.Encode())
			if _, found, e := state.Load[state.Grant](o, k); e != nil {
				return nil, e
			} else if found {
				return nil, protocol.ErrRule
			}
			if g.Key.Kind == protocol.ResourceFUEL || g.Key.Kind == protocol.ResourceCAL {
				asset := protocol.AssetFUEL
				if g.Key.Kind == protocol.ResourceCAL {
					asset = protocol.AssetCAL
				}
				accountKey := rules.AccountKey(g.Key.Account, asset)
				total, e := protocol.Add(backing[string(accountKey)], g.Amount)
				if e != nil {
					return nil, e
				}
				backing[string(accountKey)] = total
				balance, _, e := state.Load[uint64](o, accountKey)
				if e != nil {
					return nil, e
				}
				if total > balance {
					return nil, rules.ErrLimited
				}
			}
			if e = state.Put(o, k, g); e != nil {
				return nil, e
			}
		}
		if e = state.Put(o, key, engine.genesis); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	})
	return engine, e
}
func (e *Engine) verified(raw []byte) (rules.VerifiedCertificate, error) {
	key := protocol.Digest("VERIFIED_CERTIFICATE_BYTES", raw)
	if verified, ok := e.cache.get(key); ok {
		return verified, nil
	}

	c, err := protocol.DecodeCertificate(raw)
	if err != nil {
		return rules.VerifiedCertificate{}, err
	}
	org, found := e.orgs[c.Tx.Body.Config]
	if !found {
		return rules.VerifiedCertificate{}, protocol.ErrAuth
	}
	verified, err := rules.VerifyCertificate(c, org, e.cfg.Schedule)
	if err == nil {
		e.cache.put(key, verified, len(raw))
	}
	return verified, err
}
func isDirect(raw []byte) bool { return len(raw) >= 2 && binary.BigEndian.Uint16(raw[:2]) == 2 }
func (e *Engine) unwrap(raw []byte) ([]byte, error) {
	if len(raw) >= 2 && binary.BigEndian.Uint16(raw[:2]) == 82 {
		s, err := protocol.DecodeSubmission(raw)
		if err != nil {
			return nil, err
		}
		if s.Network != e.cfg.Network {
			return nil, protocol.ErrAuth
		}
		return s.Body, nil
	}
	return raw, nil
}
func (e *Engine) Check(raw []byte) error {
	if e.direct != nil {
		if protocol.IsReserveIncrease(raw) {
			c, err := protocol.DecodeReserveIncrease(raw)
			if err != nil {
				return err
			}
			if c.Network != e.cfg.Network {
				return protocol.ErrAuth
			}
			return nil
		}
		if protocol.IsClockTick(raw, e.cfg.Network) {
			return nil
		}
		if protocol.IsRepairInput(raw) {
			if e.repairCheck == nil {
				return protocol.ErrUnsupported
			}
			return e.repairCheck(raw)
		}
		_, err := e.verifyV3(raw)
		return err
	}
	var err error
	raw, err = e.unwrap(raw)
	if err != nil {
		return err
	}

	if isDirect(raw) {
		tx, err := protocol.DecodeSignedTx(raw)
		if err != nil {
			return err
		}
		_, err = rules.VerifyDirect(tx, e.cfg.Network, e.cfg.Schedule)
		return err
	}
	_, err = e.verified(raw)
	return err
}
func (e *Engine) Execute(v state.ReadView, raw []byte) (state.Transition, error) {
	if e.direct != nil {
		return state.Transition{}, protocol.ErrRule
	} // v3 requires consensus time
	var err error
	raw, err = e.unwrap(raw)
	if err != nil {
		return state.Transition{}, err
	}

	if isDirect(raw) {
		tx, err := protocol.DecodeSignedTx(raw)
		if err != nil {
			return state.Transition{}, err
		}
		verified, err := rules.VerifyDirect(tx, e.cfg.Network, e.cfg.Schedule)
		if err != nil {
			return state.Transition{}, err
		}
		tr, err := rules.EvaluateDirectTransfer(v, verified, e.cfg.Schedule)
		if err != nil {
			return tr, err
		}
		return e.enqueue(v, tr)
	}
	verified, err := e.verified(raw)
	if err != nil {
		return state.Transition{}, err
	}
	tr, err := rules.EvaluateSettlement(v, verified, e.cfg.Schedule)
	if err != nil {
		return tr, err
	}
	return e.enqueue(v, tr)
}
func (e *Engine) Account(owner protocol.Hash, asset protocol.Asset) (uint64, error) {
	var balance uint64
	err := e.db.View(func(v state.ReadView) error {
		var err error
		balance, _, err = state.Load[uint64](v, rules.AccountKey(owner, asset))
		return err
	})
	return balance, err
}
func (e *Engine) GenesisHash() protocol.Hash { return e.genesis }
