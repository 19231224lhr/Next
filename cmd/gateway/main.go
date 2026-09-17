package main

import (
	"context"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/gateway"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/protocol"
)

type configuration struct {
	Network, DataDir, Listen string
	Organization             protocol.Hash
	Members                  [4]string
	TLS                      cfg.TLS
}

func run() error {
	path := flag.String("config", "", "gateway config JSON")
	flag.Parse()
	var c configuration
	if e := cfg.Read(*path, &c); e != nil {
		return e
	}
	var n cfg.Network
	if e := cfg.Read(c.Network, &n); e != nil {
		return e
	}
	trust, e := n.Trust()
	if e != nil {
		return e
	}
	org, e := n.Organization(c.Organization)
	if e != nil {
		return e
	}
	db, e := store.Open(filepath.Join(c.DataDir, "gateway.db"), store.Identity{Network: n.Genesis.Network.String(), Role: "gateway", Node: org.Org.String(), Schema: 2})
	if e != nil {
		return e
	}
	defer db.Close()
	var clients [4]gateway.MemberClient
	for i, url := range c.Members {
		clients[i] = transport.NewMemberClient(url)
	}
	collector, e := gateway.New(org, clients, db)
	if e != nil {
		return e
	}
	mux := http.NewServeMux()
	slots := make(chan struct{}, 128)
	mux.HandleFunc("POST /v1/transactions", func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "LIMITED", 429)
			return
		}
		if r.Header.Get("Content-Type") != transport.MediaType {
			http.Error(w, "INVALID_ENCODING", 400)
			return
		}
		raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, protocol.MaxRequestBytes))
		if e != nil {
			http.Error(w, "INVALID_ENCODING", 400)
			return
		}
		request, e := protocol.DecodeRequest(raw)
		if e != nil {
			http.Error(w, "INVALID_ENCODING", 400)
			return
		}
		certificate, e := collector.Collect(r.Context(), request)
		if e != nil {
			http.Error(w, "QUORUM_UNAVAILABLE", 503)
			return
		}
		raw, e = certificate.MarshalBinary()
		if e != nil {
			http.Error(w, "INVALID_CERTIFICATE", 500)
			return
		}
		w.Header().Set("Content-Type", transport.MediaType)
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("alive\n")) })
	server, e := cfg.HTTP(c.Listen, mux, c.TLS)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	relayCtx, relayCancel := context.WithCancel(ctx)
	relay := &gateway.Relay{DB: db, Public: transport.NewCommitteeClient(n.CommitteeURLs[0]), Trust: trust, Organizations: make(map[protocol.Hash]protocol.OrgConfig), Members: make(map[protocol.Hash][4]gateway.MemberClient)}
	for _, organization := range n.Organizations {
		relay.Organizations[organization.Hash()] = organization
		var endpoints [4]gateway.MemberClient
		for i, url := range n.Members[organization.Org] {
			endpoints[i] = transport.NewMemberClient(url)
		}
		relay.Members[organization.Org] = endpoints
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
	slog.Info("gateway started", "listen", c.Listen, "organization", org.Org.String())
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
		slog.Error("gateway stopped", "error", e)
		os.Exit(1)
	}
}
