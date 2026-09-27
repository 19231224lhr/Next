package rules

import (
	"testing"
	"utxo/protocol"
)

func TestDirectPolicyRejectsAmbiguousOrganization(t *testing.T) {
	f := newDirectFixture(t)
	s := DirectSettings{Modulus: f.policy.Key.Modulus(), TimeoutSeconds: 30, RepairCost: 5}
	for _, change := range []string{"epoch", "members", "network"} {
		t.Run(change, func(t *testing.T) {
			other := f.org
			switch change {
			case "epoch":
				other.Epoch++
			case "members":
				other.Members[0], other.Members[1] = other.Members[1], other.Members[0]
			case "network":
				other.Network = protocol.Digest("other-network")
			}
			if _, err := s.Policy(f.policy.Base, []protocol.OrgConfig{f.org, other}); err == nil {
				t.Fatal("accepted two configurations for one consumption route")
			}
		})
	}
	// A member supplies its own config as well as the peer list: exact repeats
	// are the same authority, not another voting or accounting domain.
	if _, err := s.Policy(f.policy.Base, []protocol.OrgConfig{f.org, f.org}); err != nil {
		t.Fatal(err)
	}
}
