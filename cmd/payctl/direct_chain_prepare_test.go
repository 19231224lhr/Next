package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/committee"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func initializedChainLab(t *testing.T, mode string) (string, cfg.Lab, cfg.Network) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "lab")
	if err := initialize([]string{"--dir", dir, "--outputs", "1", mode}); err != nil {
		t.Fatal(err)
	}
	var lab cfg.Lab
	if err := cfg.Read(filepath.Join(dir, "lab.json"), &lab); err != nil {
		t.Fatal(err)
	}
	var n cfg.Network
	if err := cfg.Read(lab.Network, &n); err != nil {
		t.Fatal(err)
	}
	return dir, lab, n
}

func TestPrepareChainRoutingInitializedGenesis(t *testing.T) {
	for _, mode := range []string{"--v4", "--c3-final"} {
		for _, crossOrg := range []bool{false, true} {
			name := mode + "/same-org"
			if crossOrg {
				name = mode + "/cross-org"
			}
			t.Run(name, func(t *testing.T) {
				dir, lab, n := initializedChainLab(t, mode)
				original := append([]state.Grant(nil), n.Genesis.Grants...)
				if err := prepareChainRouting(dir, lab, n, crossOrg); err != nil {
					t.Fatal(err)
				}
				if err := cfg.Read(lab.Network, &n); err != nil {
					t.Fatal(err)
				}
				db := store.NewMemory()
				defer db.Close()
				if _, err := committee.NewEngine(n.Engine(), db); err != nil {
					t.Fatalf("prepared initialized genesis cannot boot: %v", err)
				}
				// Reuse the original grant verbatim, including its ID and quota.
				for _, want := range original {
					var matches []state.Grant
					for _, got := range n.Genesis.Grants {
						if got.Key == want.Key {
							matches = append(matches, got)
						}
					}
					if len(matches) != 1 || matches[0] != want {
						t.Fatalf("original grant changed or duplicated: want=%+v got=%+v", want, matches)
					}
				}
				added := 2 // Bob moved to A and Carol is new.
				bobOrg := 0
				if crossOrg {
					added, bobOrg = 1, 1 // Bob keeps his existing authorization in B.
				}
				if len(n.Genesis.Grants) != len(original)+added {
					t.Fatalf("grant count=%d want=%d", len(n.Genesis.Grants), len(original)+added)
				}
				for i, keyPath := range []string{lab.Owners[1], filepath.Join(dir, "keys", "chain-c.key")} {
					key, err := cfg.PrivateKey(keyPath)
					if err != nil {
						t.Fatal(err)
					}
					descriptor, err := boundDescriptor(n, key)
					if err != nil {
						t.Fatal(err)
					}
					orgIndex := 0
					if i == 0 {
						orgIndex = bobOrg
					}
					org := n.Organizations[orgIndex]
					if descriptor.Route.Org != org.Org {
						t.Fatal("prepared wallet bound to wrong organization")
					}
					account := protocol.Digest("POLICY", org.Org[:], descriptor.Owner[:])
					var count int
					for _, grant := range n.Genesis.Grants {
						if grant.Key == (protocol.ResourceKey{Kind: protocol.ResourcePolicy, Account: account, Version: 1}) {
							count++
							if grant.Organization != org.Hash() || grant.Subject != descriptor.Owner || grant.Amount != 1_000_000_000_000 {
								t.Fatalf("wrong wallet authorization: %+v", grant)
							}
						}
					}
					if count != 1 {
						t.Fatalf("wallet has %d policy grants, want 1", count)
					}
				}
			})
		}
	}
}

func TestPrepareChainRoutingRejectsPolicyIdentityConflict(t *testing.T) {
	for _, field := range []string{"organization", "subject"} {
		t.Run(field, func(t *testing.T) {
			dir, lab, n := initializedChainLab(t, "--v4")
			for i := range n.Genesis.Grants {
				grant := &n.Genesis.Grants[i]
				if grant.Key.Kind == protocol.ResourcePolicy && grant.Organization == n.Organizations[1].Hash() {
					if field == "organization" {
						grant.Organization = n.Organizations[0].Hash()
					} else {
						grant.Subject = protocol.PublicKey{}
					}
				}
			}
			if err := cfg.Write(lab.Network, n); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(lab.Network)
			if err != nil {
				t.Fatal(err)
			}
			if err := prepareChainRouting(dir, lab, n, true); !errors.Is(err, protocol.ErrRule) {
				t.Fatalf("conflicting policy identity must return ErrRule, got %v", err)
			}
			after, err := os.ReadFile(lab.Network)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("failed preparation persisted a changed network")
			}
		})
	}
}
