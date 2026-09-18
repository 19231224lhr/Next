package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/gateway"
	"utxo/internal/member"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/protocol"
)

type configuration struct {
	Network, DataDir, KeyFile, Listen string
	Organization                      protocol.Hash
	Index                             uint16
	Workers                           uint32
	TLS                               cfg.TLS
}

func run() error {
	path := flag.String("config", "", "member config JSON")
	flag.Parse()
	var c configuration
	if e := cfg.Read(*path, &c); e != nil {
		return e
	}
	var n cfg.Network
	if e := cfg.Read(c.Network, &n); e != nil {
		return e
	}
	org, e := n.Organization(c.Organization)
	if e != nil {
		return e
	}
	trust, e := n.Trust()
	if e != nil {
		return e
	}
	key, e := cfg.PrivateKey(c.KeyFile)
	if e != nil {
		return e
	}
	db, e := store.Open(filepath.Join(c.DataDir, "member.db"), store.Identity{Network: n.Genesis.Network.String(), Role: "member", Node: fmt.Sprintf("%s/%d", org.Org, c.Index), Schema: 2})
	if e != nil {
		return e
	}
	group, e := store.NewGroup(db, 256, 64)
	if e != nil {
		db.Close()
		return e
	}
	defer group.Close()
	m, e := member.New(member.Config{Organization: org, Index: c.Index, Key: key, Peers: n.Organizations, Committee: trust, Schedule: n.Schedule, Workers: c.Workers}, group, n.Genesis)
	if e != nil {
		return e
	}
	server, e := cfg.HTTP(c.Listen, transport.MemberHandler(m, 128, 32), c.TLS)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	relayCtx, relayCancel := context.WithCancel(ctx)
	relay := &gateway.Relay{DB: group, Public: transport.NewCommitteeClient(n.CommitteeURLs[0]), Trust: trust, Organizations: make(map[protocol.Hash]protocol.OrgConfig), Members: make(map[protocol.Hash][4]gateway.MemberClient)}
	for _, organization := range n.Organizations {
		relay.Organizations[organization.Hash()] = organization
		var endpoints [4]gateway.MemberClient
		for i, url := range n.Members[organization.Org] {
			endpoints[i] = transport.NewMemberClient(url)
		}
		relay.Members[organization.Org] = endpoints
	}
	relay.ApplyReceipts = m.ApplyReceipts
	relay.Install = func(c protocol.TXCer) error {
		if c.Tx.Body.Certifier == org.Org {
			return m.Install(c)
		}
		return nil
	}
	relayDone := make(chan error, 1)
	go func() { relayDone <- relay.Run(relayCtx) }()
	defer func() {
		relayCancel()
		if err := <-relayDone; err != nil {
			slog.Error("relay stopped", "error", err)
		}
	}()

	done := make(chan error, 1)
	go func() { done <- cfg.Serve(server) }()
	slog.Info("member started", "listen", c.Listen, "organization", org.Org.String(), "index", c.Index)
	select {
	case e = <-done:
		return e
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
func main() {
	if e := run(); e != nil {
		slog.Error("member stopped", "error", e)
		os.Exit(1)
	}
}
