package main

import (
	"testing"
	"utxo/internal/rules"
	"utxo/protocol"
)

func TestFinalC3LabAndRequestUseCALOnly(t *testing.T) {
	_, _, n, f := c3CLIFixture(t)
	// The fixture starts with legacy C3. Configure final C3 from an otherwise
	// identical clean registry to verify the explicit opt-in and quotation.
	n.Direct.C3 = nil
	n.Genesis.Grants = f.Genesis.Grants
	n.Accounts = n.Accounts[:2]
	if err := configureFinalC3Lab(&n, 2); err != nil {
		t.Fatal(err)
	}
	p, err := n.Direct.Policy(n.Schedule, n.Organizations)
	if err != nil {
		t.Fatal(err)
	}
	o := f.Genesis.Outputs[0]
	r, err := budgetRequest(n, p, f.Org, f.Owner, protocol.Input{Kind: protocol.FinalInput, Output: o.ID, Evidence: o.Fact}, o.Output, o.Output.Recipient, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Tx.Body.Fee.C3.Final() || r.Tx.Body.Fee.Source != protocol.CALService || len(r.Tx.Body.Fee.Inputs) != 0 {
		t.Fatal("final user must have no FUEL input")
	}
	if r.Tx.Body.Fee.C3.Quote != 2 || r.Tx.Body.Outputs[0].Amount+2 != o.Output.Amount {
		t.Fatal("signed CAL quote/value mismatch")
	}
	if _, err := rules.PrepareDirectVector(r.Tx, p); err != nil {
		t.Fatal(err)
	}
}
