package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/blockfollow"
	"utxo/internal/budgetprobe"
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

func run() (result error) {
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
	schema := n.Schema()
	if n.Direct != nil {
		schema = member.DirectStoreSchema
	}
	id := store.Identity{Network: n.Genesis.Network.String(), Role: "member", Node: fmt.Sprintf("%s/%d", org.Org, c.Index), Schema: schema}
	var db store.Store
	if os.Getenv("UTXO_EXPERIMENT_MEMBER_MEMORY") == "1" {
		db, e = store.OpenEphemeral(filepath.Join(c.DataDir, "member.db"), id)
	} else {
		db, e = store.OpenNoSync(filepath.Join(c.DataDir, "member.db"), id)
	}
	if e != nil {
		return e
	}
	group, e := store.NewGroup(db, 256, 64)
	if e != nil {
		db.Close()
		return e
	}
	defer func() { result = errors.Join(result, group.Close()) }()
	m, e := member.New(member.Config{Organization: org, Index: c.Index, Key: key, Peers: n.Organizations, Committee: trust, Schedule: n.Schedule, Workers: c.Workers, Direct: n.Direct}, group, n.Genesis)
	if e != nil {
		return e
	}
	handler := transport.MemberHandler(m, 256, 32)
	if os.Getenv("UTXO_EXPERIMENT_BUDGET") == "1" {
		mux := http.NewServeMux()
		mux.Handle("GET /debug/budget", budgetprobe.Handler(group, n.Genesis.Grants, org, c.Workers, m.BudgetLimits))
		mux.Handle("/", handler)
		handler = mux
	}
	server, e := cfg.HTTP(c.Listen, handler, c.TLS)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	relayCtx, relayCancel := context.WithCancel(ctx)
	relay := &gateway.Relay{DB: group, Public: transport.NewCommitteeClient(n.CommitteeURLs[0], n.CommitteeURLs[1:]...), Trust: trust, Organizations: make(map[protocol.Hash]protocol.OrgConfig), Members: make(map[protocol.Hash][4]gateway.MemberClient)}
	for _, organization := range n.Organizations {
		relay.Organizations[organization.Hash()] = organization
		var endpoints [4]gateway.MemberClient
		for i, url := range n.Members[organization.Org] {
			endpoints[i] = transport.NewMemberClient(url)
		}
		relay.Members[organization.Org] = endpoints
	}
	relay.ApplyReceipts = m.ApplyReceipts
	if n.Direct != nil {
		relay.Direct = true
		relay.MemberRelay = true
	}
	relay.Install = func(c protocol.TXCer) error {
		if c.Tx.Body.Certifier == org.Org {
			return m.Install(c)
		}
		return nil
	}
	if n.Direct != nil {
		defer blockfollow.Start(ctx, stop, group, transport.NewCommitteeClient(n.CommitteeURLs[0]), trust, m.PrepareBlock)()
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
	slog.Info("member started", "listen", c.Listen, "organization", org.Org.String(), "index", c.Index, "storage_no_sync", true, "storage_memory", os.Getenv("UTXO_EXPERIMENT_MEMBER_MEMORY") == "1")
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
