package rules

import (
	"utxo/internal/state"
	"utxo/protocol"
)

// InitializeWalletBindings installs the fixed owner-to-organization map before
// approvals or public execution. Origin outputs can declare the same binding.
func InitializeWalletBindings(o *state.Overlay, gen state.Genesis) error {
	if gen.Network == (protocol.Hash{}) {
		return protocol.ErrRule
	}
	bindings := make(map[protocol.PublicKey]protocol.Hash)
	add := func(owner protocol.PublicKey, org protocol.Hash) error {
		if owner == (protocol.PublicKey{}) || org == (protocol.Hash{}) {
			return protocol.ErrRule
		}
		if old, found := bindings[owner]; found && old != org {
			return protocol.ErrAuth
		}
		bindings[owner] = org
		return nil
	}
	for _, binding := range gen.Wallets {
		if err := add(binding.Owner, binding.Organization); err != nil {
			return err
		}
	}
	for _, origin := range gen.Outputs {
		d := origin.Output.Recipient
		if err := d.Verify(gen.Network); err != nil {
			return err
		}
		if d.Route.Kind == protocol.OrgRoute {
			if err := add(d.Owner, d.Route.Org); err != nil {
				return err
			}
		}
	}
	for owner, org := range bindings {
		old, found, err := state.Load[protocol.Hash](o, state.Key(state.KeyWalletBinding, owner[:]))
		if err != nil {
			return err
		}
		if found && old != org {
			return protocol.ErrAuth
		}
	}
	for owner, org := range bindings {
		if err := state.Put(o, state.Key(state.KeyWalletBinding, owner[:]), org); err != nil {
			return err
		}
	}
	return nil
}

func walletOrganization(v state.ReadView, owner protocol.PublicKey) (protocol.Hash, error) {
	org, found, err := state.Load[protocol.Hash](v, state.Key(state.KeyWalletBinding, owner[:]))
	if err != nil {
		return protocol.Hash{}, err
	}
	if owner == (protocol.PublicKey{}) || !found || org == (protocol.Hash{}) {
		return protocol.Hash{}, protocol.ErrAuth
	}
	return org, nil
}

func validateWalletDescriptor(v state.ReadView, network protocol.Hash, d protocol.ReceiveDescriptor) (protocol.Hash, error) {
	if err := d.Verify(network); err != nil {
		return protocol.Hash{}, err
	}
	org, err := walletOrganization(v, d.Owner)
	if err != nil {
		return protocol.Hash{}, err
	}
	if d.Route.Kind != protocol.OrgRoute || d.Route.Org != org {
		return protocol.Hash{}, protocol.ErrAuth
	}
	return org, nil
}

// ValidateWalletDescriptors checks immutable routing without requiring public
// final creations, so a wallet can validate a request before saving it.
func ValidateWalletDescriptors(v state.ReadView, tx protocol.FastTx) error {
	t := tx.Body
	if t.Kind != protocol.FastTransfer || len(tx.Claims) != len(t.Inputs) {
		return protocol.ErrRule
	}
	for _, claim := range tx.Claims {
		org, err := validateWalletDescriptor(v, t.Network, claim.Output.Recipient)
		if err != nil {
			return err
		}
		if org != t.Certifier {
			return protocol.ErrAuth
		}
	}
	for _, out := range t.Outputs {
		if _, err := validateWalletDescriptor(v, t.Network, out.Recipient); err != nil {
			return err
		}
	}
	if t.Fee.Source != protocol.OwnerFinalUTXO {
		return nil
	}
	if _, err := validateWalletDescriptor(v, t.Network, t.Fee.Refund); err != nil {
		return err
	}
	return nil
}

// ValidateWalletRoutes runs against current state even when cryptographic
// verification was cached, including the actual final user fee creations.
func ValidateWalletRoutes(v state.ReadView, tx protocol.FastTx) error {
	if err := ValidateWalletDescriptors(v, tx); err != nil {
		return err
	}
	t := tx.Body
	if t.Fee.Source != protocol.OwnerFinalUTXO {
		return nil
	}
	_, err := ValidateDirectFee(v, tx)
	return err
}

func validateCommitteeWalletRoutes(v state.ReadView, tx protocol.SignedTx) error {
	t := tx.Body
	for _, out := range t.Outputs {
		if _, err := validateWalletDescriptor(v, t.Network, out.Recipient); err != nil {
			return err
		}
	}
	if _, err := validateWalletDescriptor(v, t.Network, t.Fee.Refund); err != nil {
		return err
	}
	owners := make(map[protocol.PublicKey]bool, len(tx.Auth))
	for _, auth := range tx.Auth {
		owners[auth.Owner] = true
	}
	var inputOrg protocol.Hash
	for _, inputs := range [][]protocol.Input{t.Inputs, t.Fee.Inputs} {
		for _, in := range inputs {
			c, found, err := state.Load[state.Creation](v, state.Key(state.KeyCreation, in.Output[:]))
			if err != nil {
				return err
			}
			if !found || !c.Final || c.Fact != in.Evidence {
				return ErrMissing
			}
			d := c.Output.Recipient
			if err := d.Verify(t.Network); err != nil {
				return err
			}
			org, err := walletOrganization(v, d.Owner)
			if err != nil {
				return err
			}
			if d.Route.Kind != protocol.CommitteeRoute || !owners[d.Owner] || inputOrg != (protocol.Hash{}) && inputOrg != org {
				return protocol.ErrAuth
			}
			inputOrg = org
		}
	}
	return nil
}
