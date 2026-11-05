package main

import (
	cfg "utxo/cmd/internal/config"
	"utxo/internal/committee"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

func c3Pool(n cfg.Network, account protocol.Hash) bool {
	if n.Direct != nil && n.Direct.C3 != nil {
		for _, i := range n.Direct.C3.Issuers {
			if i.Funding.Key.Account == account {
				return true
			}
		}
	}
	return false
}
func finalC3Network(n cfg.Network) bool {
	if n.Direct == nil || n.Direct.C3 == nil {
		return false
	}
	for _, p := range n.Direct.C3.Policies {
		if p.ID == n.Direct.C3.Current {
			return p.Version == protocol.C3FinalVersion
		}
	}
	return false
}
func bindC3(body *protocol.TxBody, n cfg.Network, p rules.DirectPolicy) error {
	if p.C3 == nil {
		return nil
	}
	var refs []protocol.AdmissionRef
	for _, g := range n.Genesis.Grants {
		if g.Organization == body.Config && (g.Key.Kind != protocol.ResourcePolicy || g.Subject == body.Subject) {
			refs = append(refs, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
		}
	}
	return rules.BindC3(body, p, refs)
}

// Finite lab funding is created only in a fresh genesis. Operating receipts
// remain separate and cannot replenish the protected grant automatically.
func configureC3Lab(n *cfg.Network) error {
	if n.Direct == nil || n.Direct.C3 != nil {
		return protocol.ErrRule
	}
	id := protocol.Digest("LAB_C3_POLICY_V1")
	n.Direct.C3 = &rules.C3Settings{Current: id, Policies: []rules.C3Policy{{ID: id, Schedule: protocol.FeeSchedule{AttachReward: 1, ResolveReward: 1, Open: 1, Close: 1, Compensate: 1, Repay: 1}}}}
	for _, org := range n.Organizations {
		var subject protocol.PublicKey
		for _, g := range n.Genesis.Grants {
			if g.Organization == org.Hash() && g.Key.Kind == protocol.ResourceCAL {
				subject = g.Subject
			}
		}
		if subject == (protocol.PublicKey{}) {
			return protocol.ErrAuth
		}
		pool := protocol.Digest("C3_ISSUER_POOL", org.Org[:])
		g := state.Grant{ID: protocol.Digest("C3_ISSUER_GRANT", org.Org[:]), Organization: org.Hash(), Key: protocol.ResourceKey{Kind: protocol.ResourceFUEL, Account: pool, Version: 1}, Amount: 1000000000000, Subject: subject}
		n.Genesis.Grants = append(n.Genesis.Grants, g)
		n.Accounts = append(n.Accounts, committee.GenesisAccount{Owner: pool, Asset: protocol.AssetFUEL, Balance: g.Amount})
		n.Direct.C3.Issuers = append(n.Direct.C3.Issuers, rules.C3Issuer{Config: org.Hash(), Funding: protocol.AdmissionRef{Key: g.Key, Grant: g.ID}, Revenue: protocol.Digest("C3_OPERATING", org.Org[:]), Beneficiary: protocol.Digest("COMMITTEE_REWARDS", n.Genesis.Network[:])})
	}
	return nil
}

// Final C3 is an explicit genesis choice; --c3 continues to create legacy labs.
func configureFinalC3Lab(n *cfg.Network, baseFee uint64) error {
	if err := configureC3Lab(n); err != nil {
		return err
	}
	id := protocol.Digest("LAB_C3_POLICY_V2")
	n.Direct.C3.Current = id
	n.Direct.C3.Policies = []rules.C3Policy{{
		ID: id, Version: protocol.C3FinalVersion, BaseFee: baseFee,
		Schedule: protocol.FeeSchedule{AttachReward: 1, ResolveReward: 2, ExecuteReward: 1, Compensate: 1, Repay: 1},
		Tiers:    []rules.C3Tier{{ID: protocol.Digest("LAB_C3_PRIORITY"), CAL: 10, MinimumReward: 2}},
	}}
	trust, err := n.Trust()
	if err != nil {
		return err
	}
	for _, v := range trust.Validators.Validators {
		n.Direct.C3.Committee = append(n.Direct.C3.Committee, rules.C3Validator{ID: protocol.Digest("C3_VALIDATOR", v.Address), Address: append([]byte(nil), v.Address...), Weight: uint64(v.VotingPower)})
	}
	n.Direct.C3.EpochBlocks = 20
	n.Direct.C3.ReservedBPS = 2500
	return nil
}
