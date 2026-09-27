package config

import (
	"testing"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestNetworkRejectsAmbiguousOrganization(t *testing.T) {
	f := testkit.NewFixture("route-uniqueness", "org", 1)
	n := Network{ChainID: "route-uniqueness", Genesis: f.Genesis, Organizations: []protocol.OrgConfig{f.Org}, Committee: f.Org.Members}
	if _, err := n.Trust(); err != nil {
		t.Fatal("valid network", err)
	}
	other := f.Org
	other.Epoch++
	n.Organizations = append(n.Organizations, other)
	if _, err := n.Trust(); err == nil {
		t.Fatal("ambiguous network accepted")
	}
}
