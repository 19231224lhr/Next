package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/blockfollow"
	"utxo/internal/delivery"
	"utxo/internal/gateway"
	"utxo/internal/recipient"
	"utxo/internal/requesttrace"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type configuration struct {
	Network, DataDir, Listen string
	Organization             protocol.Hash
	Members                  [4]string
	TLS                      cfg.TLS
	RecipientRoutes          []delivery.Route   `json:",omitempty"`
	RecipientTLS             cfg.TLS            `json:",omitempty"`
	RecipientOwner           protocol.PublicKey `json:",omitempty"`
	RecipientKey             string             `json:",omitempty"`
}

func paymentHandler(collector *gateway.Collector) http.HandlerFunc {
	slots := make(chan struct{}, 128)
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if r.Header.Get(requesttrace.HeaderName) == "1" {
			ctx = requesttrace.Start(ctx, "gateway")
		}
		requesttrace.Mark(ctx, "http_handler_enter")
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
		requesttrace.Mark(ctx, "request_decoded")
		certificate, e := collector.Collect(ctx, request)
		if e != nil {
			http.Error(w, "QUORUM_UNAVAILABLE", 503)
			return
		}
		raw, e = certificate.MarshalBinary()
		if e != nil {
			http.Error(w, "INVALID_CERTIFICATE", 500)
			return
		}
		requesttrace.Mark(ctx, "response_ready")
		if requesttrace.Enabled(ctx) {
			w.Header().Set(requesttrace.HeaderName, requesttrace.Header(ctx))
		}
		w.Header().Set("Content-Type", transport.MediaType)
		// Exact length lets clients finish reading before this handler's storage work.
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
		_, _ = w.Write(raw)
		_ = http.NewResponseController(w).Flush()
		// ponytail: reuse the bounded request handler instead of another worker queue.
		// Keep its slot until persistence ends; server shutdown drains active handlers.
		if err := collector.Persist(certificate); err != nil {
			slog.Error("background certificate persistence failed; wallet may resubmit", "spend", protocol.Hash(certificate.QC.Fact).String(), "error", err)
		}
	}
}

func run() (result error) {
	path := flag.String("config", "", "gateway config JSON")
	continuation := flag.Bool("same-org-continuation", false, "send compact parent quorum proofs to their original same-organization signers")
	compactApprovals := flag.Bool("compact-direct-approvals", false, "request compact admission-vector and vote responses")
	directDelivery := flag.Bool("direct-recipient-delivery", false, "durably deliver full payments to operator-configured RecipientRoutes")
	collectAtRecipient := flag.Bool("collect-at-recipient", false, "enable /v3/recipient/collect for configured RecipientOwner with a durable local receiving wallet")
	flag.Parse()
	var c configuration
	if e := cfg.Read(*path, &c); e != nil {
		return e
	}
	var n cfg.Network
	if e := cfg.Read(c.Network, &n); e != nil {
		return e
	}
	if *collectAtRecipient {
		if n.Direct == nil || c.RecipientKey == "" || os.Getenv("UTXO_EXPERIMENT_GATEWAY_MEMORY") == "1" {
			return errors.New("recipient collection requires direct protocol, RecipientKey and persistent gateway storage")
		}
		key, err := cfg.PrivateKey(c.RecipientKey)
		if err != nil {
			return err
		}
		var owner protocol.PublicKey
		copy(owner[:], key[32:])
		if c.RecipientOwner != (protocol.PublicKey{}) && c.RecipientOwner != owner {
			return errors.New("RecipientOwner does not match RecipientKey")
		}
		c.RecipientOwner = owner
	}
	trust, e := n.Trust()
	if e != nil {
		return e
	}
	org, e := n.Organization(c.Organization)
	if e != nil {
		return e
	}
	id := store.Identity{Network: n.Genesis.Network.String(), Role: "gateway", Node: org.Org.String(), Schema: n.Schema()}
	var base store.Store
	if os.Getenv("UTXO_EXPERIMENT_GATEWAY_MEMORY") == "1" {
		base, e = store.OpenEphemeral(filepath.Join(c.DataDir, "gateway.db"), id)
	} else {
		base, e = store.Open(filepath.Join(c.DataDir, "gateway.db"), id)
	}
	if e != nil {
		return e
	}
	db, e := store.NewGroup(base, 256, 64)
	if e != nil {
		base.Close()
		return e
	}
	defer func() { result = errors.Join(result, db.Close()) }()
	var clients [4]gateway.MemberClient
	for i, url := range c.Members {
		clients[i] = transport.NewMemberClient(url)
	}
	collector, e := gateway.New(org, clients, db)
	if e != nil {
		return e
	}
	collector.SameOrgContinuation = *continuation
	collector.CompactDirectApprovals = *compactApprovals
	if os.Getenv("UTXO_EXPERIMENT_SERIAL_DIRECT") == "1" {
		collector.EnableSerialDirect()
		slog.Info("experimental single-collector signing admission enabled")
	}
	mux := http.NewServeMux()
	if *collectAtRecipient {
		walletDB, err := store.Open(filepath.Join(c.DataDir, "recipient-wallet.db"), store.Identity{Network: n.Genesis.Network.String(), Role: "recipient-wallet", Node: fmt.Sprintf("%x", c.RecipientOwner), Schema: 1})
		if err != nil {
			return err
		}
		defer walletDB.Close()
		local, err := wallet.NewWithGenesis(walletDB, n.Genesis.Network, c.RecipientOwner, n.Organizations, n.Genesis)
		if err != nil {
			return err
		}
		h, err := recipient.New(collector, local, c.RecipientOwner, org, db)
		if err != nil {
			return err
		}
		defer h.Close()
		mux.Handle("POST /v3/recipient/collect", h)
		mux.HandleFunc("POST /v3/recipient/prepare", h.PrepareHTTP)
	}
	requesttrace.RegisterTimeline(mux)
	mux.HandleFunc("POST /v1/transactions", paymentHandler(collector))
	drainDirect := func() {}
	var recipientOutbox *delivery.Outbox
	if *directDelivery {
		if n.Direct == nil || len(c.RecipientRoutes) == 0 {
			return errors.New("direct recipient delivery requires direct protocol and RecipientRoutes")
		}
		deliveryDB, err := store.Open(filepath.Join(c.DataDir, "recipient-delivery.db"), store.Identity{Network: n.Genesis.Network.String(), Role: "recipient-delivery", Node: org.Org.String(), Schema: 1})
		if err != nil {
			return err
		}
		defer deliveryDB.Close()
		client, err := recipientHTTP(c.RecipientTLS)
		if err != nil {
			return err
		}
		recipientOutbox, err = delivery.NewWithClient(deliveryDB, c.RecipientRoutes, n.Organizations, client)
		if err != nil {
			return err
		}
	}
	if n.Direct != nil {
		var handler http.HandlerFunc
		handler, drainDirect = newDirectPaymentHandlerWithDelivery(collector, true, recipientOutbox)
		mux.HandleFunc("POST /v3/transactions", handler)
		if os.Getenv("UTXO_EXPERIMENT_BUDGET") == "1" {
			collectOnly, drainCollect := newDirectPaymentHandler(collector, false)
			mux.HandleFunc("POST /debug/budget/collect", collectOnly)
			drainPayment := drainDirect
			drainDirect = func() { drainPayment(); drainCollect() }
		}
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("alive\n")) })
	server, e := cfg.HTTP(c.Listen, mux, c.TLS)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if recipientOutbox != nil {
		deliveryCtx, deliveryCancel := context.WithCancel(ctx)
		deliveryDone := make(chan error, 1)
		go func() {
			err := recipientOutbox.Run(deliveryCtx)
			if err != nil {
				slog.Error("recipient delivery stopped", "error", err)
				stop()
			}
			deliveryDone <- err
		}()
		defer func() { deliveryCancel(); result = errors.Join(result, <-deliveryDone) }()
	}
	relayCtx, relayCancel := context.WithCancel(ctx)
	relay := &gateway.Relay{DB: db, Public: transport.NewCommitteeClient(n.CommitteeURLs[0], n.CommitteeURLs[1:]...), Trust: trust, Organizations: make(map[protocol.Hash]protocol.OrgConfig), Members: make(map[protocol.Hash][4]gateway.MemberClient)}
	relay.Direct = n.Direct != nil
	if relay.Direct {
		relay.Early = gateway.NewDirectInbox()
		collector.OfferDirect = relay.Early.Offer
		wake := make(chan protocol.SpendFactID, 256)
		relay.Wake = wake
		collector.NotifyPersisted = func(fact protocol.SpendFactID) {
			select {
			case wake <- fact:
			default:
			}
		}
	}
	for _, organization := range n.Organizations {
		relay.Organizations[organization.Hash()] = organization
		var endpoints [4]gateway.MemberClient
		for i, url := range n.Members[organization.Org] {
			endpoints[i] = transport.NewMemberClient(url)
		}
		relay.Members[organization.Org] = endpoints
	}

	if n.Direct != nil {
		defer blockfollow.Start(ctx, stop, db, transport.NewCommitteeClient(n.CommitteeURLs[0]), trust, relay.ObserveBlock(org.Hash()))()
	}
	relayDone := make(chan error, 1)
	go func() { relayDone <- relay.Run(relayCtx) }()
	defer func() {
		relayCancel()
		if err := <-relayDone; err != nil {
			slog.Error("relay stopped", "error", err)
		}
	}()

	defer drainDirect() // Handlers/saves finish before relay, follower and database cleanup.
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
