package member

import (
	"bytes"
	"crypto/ed25519"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type Config struct {
	Organization protocol.OrgConfig
	Index        uint16
	Key          ed25519.PrivateKey
	Peers        []protocol.OrgConfig
	Schedule     rules.Schedule
	Workers      uint32
}
type Request struct {
	Tx      protocol.SignedTx
	Parents []protocol.TXCer
}
type Approval struct {
	Fact      protocol.SpendFactID
	Vote      protocol.SpendVote
	Admission protocol.AdmissionVector
	Effects   protocol.CertifiedEffects
}
type Member struct {
	cfg   Config
	db    store.Store
	peers map[protocol.Hash]protocol.OrgConfig
}

func New(cfg Config, db store.Store, gen state.Genesis) (*Member, error) {
	if cfg.Organization.Validate() != nil || cfg.Index >= 4 || len(cfg.Key) != ed25519.PrivateKeySize || cfg.Workers == 0 || cfg.Workers > 256 || cfg.Schedule.Validate() != nil || gen.Network != cfg.Organization.Network {
		return nil, protocol.ErrRule
	}
	if !bytes.Equal(cfg.Key.Public().(ed25519.PublicKey), cfg.Organization.Members[cfg.Index][:]) {
		return nil, protocol.ErrAuth
	}
	cfg.Key = bytes.Clone(cfg.Key)
	m := &Member{cfg: cfg, db: db, peers: make(map[protocol.Hash]protocol.OrgConfig)}
	for _, p := range cfg.Peers {
		if p.Validate() != nil || p.Network != gen.Network {
			return nil, protocol.ErrAuth
		}
		m.peers[p.Hash()] = p
	}
	m.peers[cfg.Organization.Hash()] = cfg.Organization
	if e := m.bootstrap(gen); e != nil {
		return nil, e
	}
	return m, nil
}
func (m *Member) bootstrap(gen state.Genesis) error {
	return m.db.Update(func(v state.ReadView) ([]state.Change, error) {
		key := state.Key(state.KeyGenesis)
		hash := gen.Hash()
		if old, found, e := state.Load[protocol.Hash](v, key); e != nil {
			return nil, e
		} else if found {
			if old != hash {
				return nil, store.ErrIdentity
			}
			return nil, nil
		}
		o := state.NewOverlay(v)
		seen := make(map[protocol.OutputID]bool)
		for _, g := range gen.Outputs {
			if seen[g.ID] || g.ID == (protocol.OutputID{}) || g.Fact == (protocol.Hash{}) || g.Output.Amount == 0 || g.Output.Recipient.Verify(gen.Network) != nil {
				return nil, protocol.ErrRule
			}
			seen[g.ID] = true
			if e := state.Put(o, state.Key(state.KeyCreation, g.ID[:]), state.Creation{Output: g.Output, Fact: g.Fact, Final: true}); e != nil {
				return nil, e
			}
		}
		for _, g := range gen.Grants {
			if g.Organization != m.cfg.Organization.Hash() {
				continue
			}
			if g.ID == (protocol.Hash{}) || g.Key.Kind < protocol.ResourceFUEL || g.Key.Kind > protocol.ResourcePolicy || g.Key.Version == 0 {
				return nil, protocol.ErrRule
			}
			if _, found, e := state.Load[state.Grant](o, state.Key(state.KeyGrant, g.Key.Encode())); e != nil {
				return nil, e
			} else if found {
				return nil, protocol.ErrRule
			}
			if e := state.Put(o, state.Key(state.KeyGrant, g.Key.Encode()), g); e != nil {
				return nil, e
			}
			share, e := protocol.GrantShare(g.Amount)
			if e != nil {
				return nil, e
			}
			for w := uint32(0); w < m.cfg.Workers; w++ {
				amount := share / uint64(m.cfg.Workers)
				if uint64(w) < share%uint64(m.cfg.Workers) {
					amount++
				}
				if e = state.Put(o, state.SliceKey(g.Key, w), state.Slice{Available: amount, Generation: 1}); e != nil {
					return nil, e
				}
			}
		}
		if e := state.Put(o, key, hash); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	})
}
func (m *Member) verify(c protocol.TXCer) error {
	p, ok := m.peers[c.Tx.Body.Config]
	if !ok {
		return protocol.ErrAuth
	}
	return c.Verify(p)
}
func (m *Member) Approve(req Request) (Approval, error) {
	tx := req.Tx
	t := tx.Body
	cfg := m.cfg.Organization
	if e := tx.VerifyAuth(); e != nil {
		return Approval{}, e
	}
	if t.Network != cfg.Network || t.Certifier != cfg.Org || t.Epoch != cfg.Epoch || t.Config != cfg.Hash() {
		return Approval{}, protocol.ErrAuth
	}
	vector, e := rules.PrepareVector(t, m.cfg.Schedule)
	if e != nil {
		return Approval{}, e
	}
	if len(req.Parents) > protocol.MaxInputs {
		return Approval{}, protocol.ErrEncoding
	}
	parents := make(map[protocol.Hash]protocol.TXCer)
	materials := make([]state.Outbox, 0, len(req.Parents))
	total := 0
	for _, c := range req.Parents {
		if e = m.verify(c); e != nil {
			return Approval{}, e
		}
		raw, e := c.MarshalBinary()
		if e != nil {
			return Approval{}, e
		}
		total += len(raw)
		if total > protocol.MaxEvidenceBytes {
			return Approval{}, protocol.ErrEncoding
		}
		id := protocol.Hash(c.QC.Fact)
		if _, found := parents[id]; found {
			return Approval{}, protocol.ErrRule
		}
		parents[id] = c
		materials = append(materials, state.Outbox{Fact: c.QC.Fact, Certificate: raw, Origin: c.Tx.Body.Certifier})
	}
	inputs := make([]state.Creation, len(t.Inputs))
	depth := uint32(1)
	ancestors := uint64(1)
	usedParents := make(map[protocol.Hash]bool)
	e = m.db.View(func(v state.ReadView) error {
		for i, in := range t.Inputs {
			if in.Kind == protocol.FinalInput {
				record, found, e := state.Load[state.Creation](v, state.Key(state.KeyCreation, in.Output[:]))
				if e != nil {
					return e
				}
				if !found || !record.Final || record.Fact != in.Evidence {
					return rules.ErrMissing
				}
				inputs[i] = record
			} else {
				c, ok := parents[in.Evidence]
				if !ok {
					return rules.ErrMissing
				}
				usedParents[in.Evidence] = true
				found := false
				for j, id := range c.Effects.Outputs {
					if id == in.Output {
						inputs[i] = state.Creation{Output: c.Tx.Body.Outputs[j], Fact: in.Evidence}
						found = true
						break
					}
				}
				if !found {
					return rules.ErrMissing
				}
				if c.Effects.Depth >= t.Work.Depth {
					return rules.ErrLimited
				}
				if c.Effects.Depth+1 > depth {
					depth = c.Effects.Depth + 1
				}
				ancestors += uint64(c.Effects.Ancestors)
				if ancestors > uint64(t.Work.Ancestors) {
					return rules.ErrLimited
				}
			}
		}
		return nil
	})
	if e != nil {
		return Approval{}, e
	}
	if len(usedParents) != len(parents) {
		return Approval{}, protocol.ErrRule
	}
	if e = rules.ValidateTransfer(tx, inputs); e != nil {
		return Approval{}, e
	}
	effects := protocol.EffectsFor(t, depth, uint32(ancestors))
	id := t.ID()
	worker := uint32(id[0]) % m.cfg.Workers
	var fact protocol.SpendFactID
	e = m.db.Update(func(v state.ReadView) ([]state.Change, error) {
		cs, f, err := rules.EvaluatePrepare(v, tx, inputs, vector, effects, worker, materials)
		fact = f
		return cs, err
	})
	if e != nil {
		return Approval{}, e
	}
	// Signing occurs only after the authoritative storage commit succeeds.
	return Approval{Fact: fact, Vote: protocol.SignSpend(fact, m.cfg.Index, m.cfg.Key), Admission: vector, Effects: effects}, nil
}
func (m *Member) Install(c protocol.TXCer) error {
	if e := m.verify(c); e != nil {
		return e
	}
	if c.Tx.Body.Certifier != m.cfg.Organization.Org {
		return protocol.ErrAuth
	}
	raw, e := c.MarshalBinary()
	if e != nil {
		return e
	}
	return m.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		for _, id := range c.Effects.Inputs {
			spend, _, e := state.Load[state.Spend](o, state.Key(state.KeySpend, id[:]))
			if e != nil {
				return nil, e
			}
			if spend.Consumed != (protocol.SpendFactID{}) && spend.Consumed != c.QC.Fact {
				return nil, rules.ErrConflict
			}
			spend.Consumed = c.QC.Fact
			if e = state.Put(o, state.Key(state.KeySpend, id[:]), spend); e != nil {
				return nil, e
			}
		}
		for i, id := range c.Effects.Outputs {
			creation := state.Creation{Output: c.Tx.Body.Outputs[i], Fact: protocol.Hash(c.QC.Fact)}
			old, found, e := state.Load[state.Creation](o, state.Key(state.KeyCreation, id[:]))
			if e != nil {
				return nil, e
			}
			if found {
				if old.Output != creation.Output {
					return nil, protocol.ErrRule
				}
			} else if e = state.Put(o, state.Key(state.KeyCreation, id[:]), creation); e != nil {
				return nil, e
			}
		}
		o.Set(state.Key(state.KeyInstall, c.QC.Fact[:]), raw)
		if e := state.Put(o, state.Key(state.KeyOutbox, c.QC.Fact[:]), state.Outbox{Fact: c.QC.Fact, Certificate: raw, Origin: c.Tx.Body.Certifier}); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	})
}
func (m *Member) Certificate(f protocol.SpendFactID) (protocol.TXCer, error) {
	var raw []byte
	e := m.db.View(func(v state.ReadView) error { b, e := v.Get(state.Key(state.KeyInstall, f[:])); raw = b; return e })
	if e != nil {
		return protocol.TXCer{}, e
	}
	return protocol.DecodeCertificate(raw)
}
func (m *Member) Quota(k protocol.ResourceKey, worker uint32) (state.Slice, error) {
	var out state.Slice
	e := m.db.View(func(v state.ReadView) error {
		x, found, e := state.Load[state.Slice](v, state.SliceKey(k, worker))
		if e != nil {
			return e
		}
		if !found {
			return state.ErrNotFound
		}
		out = x
		return nil
	})
	return out, e
}
