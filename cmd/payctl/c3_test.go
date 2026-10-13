package main

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestC3LabBuilderRetainsIssuerPool(t *testing.T) {
	f := testkit.NewFixture("c3-lab", "org", 1)
	f.EnableDirect()
	n := cfg.Network{Genesis: f.Genesis, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Direct: &rules.DirectSettings{}}
	if e := configureC3Lab(&n); e != nil {
		t.Fatal(e)
	}
	if len(n.Direct.C3.Issuers) != 1 {
		t.Fatal("issuer pool absent")
	}
	pool := n.Direct.C3.Issuers[0].Funding.Key.Account
	if !c3Pool(n, pool) {
		t.Fatal("pool not protected during owner-FUEL preparation")
	}
	raw, e := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if e != nil {
		t.Fatal(e)
	}
	block, _ := pem.Decode(raw)
	key, e := x509.ParsePKCS1PrivateKey(block.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	n.Direct.Modulus = key.N.Bytes()
	n.Direct.TimeoutSeconds = 30
	n.Direct.RepairCost = 5
	p, e := n.Direct.Policy(n.Schedule, n.Organizations)
	if e != nil {
		t.Fatal(e)
	}
	origin := f.Genesis.Outputs[0]
	for _, owner := range []bool{false, true} {
		var fuel []state.OriginOutput
		if owner {
			fuel = []state.OriginOutput{{ID: protocol.OutputID(protocol.Digest("fuel")), Fact: protocol.Digest("fuel-final"), Output: protocol.Output{Asset: protocol.AssetFUEL, Amount: 10000, Recipient: origin.Output.Recipient}}}
		}
		r, e := budgetRequest(n, p, f.Org, f.Owner, protocol.Input{Kind: protocol.FinalInput, Output: origin.ID, Evidence: origin.Fact}, origin.Output, origin.Output.Recipient, nil, fuel...)
		if e != nil {
			t.Fatal(e)
		}
		v, e := rules.PrepareDirectVector(r.Tx, p)
		if e != nil {
			t.Fatal(e)
		}
		count := 0
		for _, a := range v {
			if a.Key.Kind == protocol.ResourceFUEL {
				count++
			}
		}
		want := 2
		if owner {
			want = 1
		}
		if count != want || !r.Tx.Body.Fee.C3.Enabled() {
			t.Fatalf("fee purpose separation: owner=%v rows=%d", owner, count)
		}
	}
}
