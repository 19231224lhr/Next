package config

import (
	"context"
	"log/slog"
	"utxo/internal/gateway"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/protocol"
)

// StartWalletRelay resumes the wallet's existing durable outbox. Stop it before
// closing its store. It needs no gateway and never releases pending input locks.
func (n Network) StartWalletRelay(db store.Store) (func(), error) {
	trust, err := n.Trust()
	if err != nil {
		return nil, err
	}
	relay := gateway.Relay{DB: db, Public: transport.NewCommitteeClient(n.CommitteeURLs[0], n.CommitteeURLs[1:]...), Trust: trust, Organizations: make(map[protocol.Hash]protocol.OrgConfig), Members: make(map[protocol.Hash][4]gateway.MemberClient)}
	for _, org := range n.Organizations {
		relay.Organizations[org.Hash()] = org
		var clients [4]gateway.MemberClient
		for i, url := range n.Members[org.Org] {
			clients[i] = transport.NewMemberClient(url)
		}
		relay.Members[org.Org] = clients
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := relay.Run(ctx); err != nil {
			slog.Error("wallet relay stopped", "error", err)
		}
	}()
	return func() { cancel(); <-done }, nil
}
