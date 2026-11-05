package main

import (
	"os"
	"path/filepath"
	"testing"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

// Isolate component CPU cost using an actual signed request from a completed
// chain. Setup and copying the wallet are outside benchmark timers.
func BenchmarkChainPreparation(b *testing.B) {
	dir := os.Getenv("UTXO_CHAIN_PROFILE_LAB")
	if dir == "" {
		b.Skip("set UTXO_CHAIN_PROFILE_LAB to a completed chain laboratory")
	}
	var lab cfg.Lab
	var network cfg.Network
	var report chainReport
	for path, target := range map[string]any{
		filepath.Join(dir, "lab.json"):                 &lab,
		filepath.Join(dir, "config", "network.json"):   &network,
		filepath.Join(dir, "reports", "chain-v4.json"): &report,
	} {
		if err := cfg.Read(path, target); err != nil {
			b.Fatal(err)
		}
	}
	h := report.Hops[1] // Bob's signed request spends Alice's real first receipt.
	if h.Sender != 1 || !h.CertificateInput {
		b.Fatal("expected a certified input owned by Bob")
	}
	contents, err := os.ReadFile(filepath.Join(dir, "chain-wallet-1.db"))
	if err != nil {
		b.Fatal(err)
	}
	copyPath := filepath.Join(b.TempDir(), "wallet.db")
	if err = os.WriteFile(copyPath, contents, 0600); err != nil {
		b.Fatal(err)
	}
	db, err := store.OpenNoSync(copyPath, store.Identity{Network: network.ChainID, Role: "chain-wallet", Node: "1", Schema: 4})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	var raw []byte
	if err = db.View(func(v state.ReadView) error {
		var e error
		raw, e = v.Get(state.Key(state.KeyWalletRequest, h.Tx[:]))
		return e
	}); err != nil {
		b.Fatal(err)
	}
	request, err := protocol.DecodeDirectRequest(raw)
	if err != nil {
		b.Fatal(err)
	}
	policy, err := network.Direct.Policy(network.Schedule, network.Organizations)
	if err != nil {
		b.Fatal(err)
	}
	key, err := cfg.PrivateKey(lab.Owners[1])
	if err != nil {
		b.Fatal(err)
	}
	id := request.Tx.ID()
	b.Run("generate_transaction", func(b *testing.B) {
		for b.Loop() {
			if _, err := protocol.NewFastTx(request.Tx.Body, request.Tx.Claims, policy.Key); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("sign_transaction", func(b *testing.B) {
		for b.Loop() {
			protocol.SignOwner(id, key)
		}
	})
	b.Run("encode_request", func(b *testing.B) {
		for b.Loop() {
			if _, err := request.MarshalBinary(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("verify_transaction_auth", func(b *testing.B) {
		for b.Loop() {
			if err := request.Tx.VerifyAuth(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("validate_wallet_descriptors", func(b *testing.B) {
		for b.Loop() {
			if err := db.View(func(v state.ReadView) error { return rules.ValidateWalletDescriptors(v, request.Tx) }); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("transaction_id", func(b *testing.B) {
		for b.Loop() {
			request.Tx.ID()
		}
	})
}
