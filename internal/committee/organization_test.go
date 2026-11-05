package committee

import (
	"testing"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestEngineRejectsAmbiguousOrganization(t *testing.T) {
	for _, direct := range []bool{false, true} {
		cfg, _, _, _ := cacheFixture(t, 1)
		if !direct {
			cfg.Direct = nil
			f := testkit.NewFixture("verify-cache", "legacy", 1)
			cfg.Organizations = []protocol.OrgConfig{f.Org}
			cfg.Genesis = f.Genesis
			cfg.Accounts = []GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1_000_000_000}}
		}
		baseline := store.NewMemory()
		if _, err := NewEngine(cfg, baseline); err != nil {
			t.Fatalf("valid baseline direct=%v: %v", direct, err)
		}
		baseline.Close()
		other := testkit.NewFixture("verify-cache", "disjoint-members", 1).Org
		other.Org = cfg.Organizations[0].Org
		cfg.Organizations = append(cfg.Organizations, other)
		db := store.NewMemory()
		_, err := NewEngine(cfg, db)
		db.Close()
		if err == nil {
			t.Fatalf("direct=%v: accepted disjoint members for one organization", direct)
		}
	}
}
