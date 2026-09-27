package wallet

import (
	"testing"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestWalletRejectsAmbiguousOrganization(t *testing.T) {
	f := testkit.NewFixture("wallet-route", "a", 1)
	db := store.NewMemory()
	defer db.Close()
	owner := f.Genesis.Outputs[0].Output.Recipient.Owner
	if _, err := New(db, f.Org.Network, owner, []protocol.OrgConfig{f.Org}); err != nil {
		t.Fatal(err)
	}
	other := f.Org
	other.Epoch++
	if _, err := New(db, f.Org.Network, owner, []protocol.OrgConfig{f.Org, other}); err == nil {
		t.Fatal("accepted conflicting wallet authorities")
	}
}
