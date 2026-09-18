package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/store"
)

func relayWallet(args []string) error {
	flags := flag.NewFlagSet("wallet-relay", flag.ContinueOnError)
	dir := flags.String("dir", "", "laboratory directory")
	owner := flags.Int("owner", 0, "wallet index (0 or 1); wallet must not be open elsewhere")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *owner < 0 || *owner > 1 {
		return fmt.Errorf("owner must be 0 or 1")
	}
	var lab cfg.Lab
	if err := cfg.Read(filepath.Join(*dir, "lab.json"), &lab); err != nil {
		return err
	}
	var n cfg.Network
	if err := cfg.Read(lab.Network, &n); err != nil {
		return err
	}
	db, err := store.Open(filepath.Join(*dir, fmt.Sprintf("wallet%d.db", *owner)), store.Identity{Network: n.Genesis.Network.String(), Role: "wallet", Node: fmt.Sprint(*owner), Schema: 2})
	if err != nil {
		return err
	}
	defer db.Close()
	stop, err := n.StartWalletRelay(db)
	if err != nil {
		return err
	}
	defer stop()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fmt.Println("Wallet relay active; Ctrl-C to stop.")
	<-ctx.Done()
	return nil
}
