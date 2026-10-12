package main

import (
	"crypto/ed25519"
	"fmt"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/state"
	"utxo/protocol"
)

func boundDescriptor(n cfg.Network, key ed25519.PrivateKey) (protocol.ReceiveDescriptor, error) {
	var owner protocol.PublicKey
	copy(owner[:], key[32:])
	var organization protocol.Hash
	accept := func(org protocol.Hash) error {
		if organization != (protocol.Hash{}) && organization != org {
			return fmt.Errorf("wallet has contradictory genesis routes")
		}
		organization = org
		return nil
	}
	for _, binding := range n.Genesis.Wallets {
		if binding.Owner == owner {
			if err := accept(binding.Organization); err != nil {
				return protocol.ReceiveDescriptor{}, err
			}
		}
	}
	for _, origin := range n.Genesis.Outputs {
		if origin.Output.Recipient.Owner == owner && origin.Output.Recipient.Route.Kind == protocol.OrgRoute {
			if err := accept(origin.Output.Recipient.Route.Org); err != nil {
				return protocol.ReceiveDescriptor{}, err
			}
		}
	}
	if organization == (protocol.Hash{}) {
		return protocol.ReceiveDescriptor{}, fmt.Errorf("wallet is not bound in genesis")
	}
	return protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: organization}, key), nil
}

// This fixture helper runs only before any laboratory node starts.
// It updates every genesis output together with the explicit wallet binding.
func bindFixtureWallet(n *cfg.Network, descriptor protocol.ReceiveDescriptor) {
	for i := range n.Genesis.Outputs {
		if n.Genesis.Outputs[i].Output.Recipient.Owner == descriptor.Owner {
			n.Genesis.Outputs[i].Output.Recipient = descriptor
		}
	}
	for i := range n.Genesis.Wallets {
		if n.Genesis.Wallets[i].Owner == descriptor.Owner {
			n.Genesis.Wallets[i].Organization = descriptor.Route.Org
			return
		}
	}
	n.Genesis.Wallets = append(n.Genesis.Wallets, state.WalletBinding{Owner: descriptor.Owner, Organization: descriptor.Route.Org})
}
