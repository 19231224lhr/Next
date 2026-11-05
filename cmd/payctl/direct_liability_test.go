package main

import (
	"testing"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/committee"
	"utxo/internal/state"
	"utxo/protocol"
)

func TestLiabilityFixtureUsesUserFuelAndFixedReserves(t *testing.T) {
	org := protocol.Hash{1}
	n := cfg.Network{Genesis: state.Genesis{Grants: []state.Grant{{Key: protocol.ResourceKey{Kind: protocol.ResourceCAL}, Amount: 1}, {Key: protocol.ResourceKey{Kind: protocol.ResourceFUEL}}, {Key: protocol.ResourceKey{Kind: protocol.ResourcePolicy}}}}, Accounts: []committee.GenesisAccount{{Owner: org, Asset: protocol.AssetCAL, Balance: 1}, {Owner: org, Asset: protocol.AssetFUEL, Balance: 99}}}
	var desc [3]protocol.ReceiveDescriptor
	for i := range desc {
		desc[i].Owner[0] = byte(i + 1)
	}
	n = liabilityGenesis(n, desc)
	if len(n.Genesis.Grants) != 1 || n.Genesis.Grants[0].Amount != 60000 || len(n.Accounts) != 1 || n.Accounts[0].Balance != 60000 {
		t.Fatal("wrong reserve or sponsor configuration")
	}
	if len(n.Genesis.Outputs) != 3 {
		t.Fatal("three independent fee inputs required")
	}
	seen := map[protocol.OutputID]bool{}
	for i, o := range n.Genesis.Outputs {
		if o.Output.Asset != protocol.AssetFUEL || o.Output.Amount != 10000 || o.Output.Recipient != desc[i] || seen[o.ID] {
			t.Fatal("wrong or duplicate user funding")
		}
		seen[o.ID] = true
	}
}
