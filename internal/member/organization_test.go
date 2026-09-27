package member_test

import (
	"testing"
	"utxo/internal/member"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestMemberRejectsAmbiguousOrganization(t *testing.T) {
	f := setup(t)
	other := f.cfg
	other.Epoch++
	db := store.NewMemory()
	defer db.Close()
	_, err := member.New(member.Config{Organization: f.cfg, Index: 0, Key: f.keys[0], Peers: []protocol.OrgConfig{other}, Schedule: f.schedule, Workers: 1}, db, f.gen)
	if err == nil {
		t.Fatal("accepted a second configuration for the local consumption route")
	}
}
