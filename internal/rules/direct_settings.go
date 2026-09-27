package rules

import (
	"crypto/rsa"
	"math/big"
	"utxo/crypto/chameleon"
	"utxo/protocol"
)

// DirectSettings is genesis-bound and contains no private key material.
type DirectSettings struct {
	Modulus        []byte
	TimeoutSeconds int64
	RepairCost     uint64
}

func (s DirectSettings) Policy(base Schedule, organizations []protocol.OrgConfig) (DirectPolicy, error) {
	p := DirectPolicy{Base: base, TimeoutSeconds: s.TimeoutSeconds, RepairCost: s.RepairCost, Organizations: map[protocol.Hash]protocol.OrgConfig{}}
	if len(organizations) > 0 {
		if err := protocol.ValidateOrganizations(organizations[0].Network, organizations); err != nil {
			return p, err
		}
	}
	if len(s.Modulus) != chameleon.Size || s.TimeoutSeconds <= 0 || s.RepairCost == 0 || base.Validate() != nil {
		return p, protocol.ErrRule
	}
	key, err := chameleon.NewPublic(&rsa.PublicKey{N: new(big.Int).SetBytes(s.Modulus), E: 65537})
	if err != nil {
		return p, err
	}
	p.Key = key
	for _, org := range organizations {
		p.Organizations[org.Hash()] = org
	}
	return p, nil
}
