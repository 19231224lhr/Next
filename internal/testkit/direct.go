package testkit

import (
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

// EnableDirect adds CAL coverage to the finite laboratory genesis.
func (f *Fixture) EnableDirect() {
	g := state.Grant{ID: protocol.Digest("LAB_GRANT", f.Org.Org[:], []byte{byte(protocol.ResourceCAL)}), Organization: f.Org.Hash(), Key: protocol.ResourceKey{Kind: protocol.ResourceCAL, Account: f.Org.Org, Version: 1}, Amount: 1000000000, Subject: f.Genesis.Outputs[0].Output.Recipient.Owner}
	f.Genesis.Grants = append([]state.Grant{g}, f.Genesis.Grants...)
}
func (f Fixture) FastTransaction(index int, nonce uint64, p rules.DirectPolicy) (protocol.FastTx, error) {
	// Reuse the fixture's ordinary transaction and replace only v3 semantics.
	b := f.Transaction(index, nonce).Body
	b.Wire = 4
	b.Version = 4
	b.Rules = p.Rules()
	b.Fee.Maximum = 1000
	b.Work.Depth = 1
	b.Work.Ancestors = 1
	b.Work.Bytes = 10000000
	for _, g := range f.Genesis.Grants {
		if g.Key.Kind == protocol.ResourcePolicy {
			b.Fee.Policy = g.Key.Account
		}
	}
	b.Intent = b.IntentID()
	tx, err := protocol.NewFastTx(b, []protocol.InputClaim{{Output: f.Genesis.Outputs[index].Output}}, p.Key)
	if err != nil {
		return tx, err
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.Owner)}
	return tx, nil
}
func (f Fixture) DirectCertificate(tx protocol.FastTx, p rules.DirectPolicy) (protocol.OutputCertificate, error) {
	vector, err := rules.PrepareDirectVector(tx, p)
	if err != nil {
		return protocol.OutputCertificate{}, err
	}
	c := protocol.OutputCertificate{Summary: protocol.SummaryFor(tx, vector)}
	c.QC.Fact = c.Summary.Fact()
	for i := 0; i < 3; i++ {
		c.QC.Votes = append(c.QC.Votes, protocol.SignSpend(c.QC.Fact, uint16(i), f.Keys[i]))
	}
	return c, c.Verify(f.Org)
}
