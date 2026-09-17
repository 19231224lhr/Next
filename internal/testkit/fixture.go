// Package testkit builds finite, reproducible laboratory identities and funds.
package testkit

import (
	"crypto/ed25519"
	"fmt"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type Fixture struct {
	Org      protocol.OrgConfig
	Keys     [4]ed25519.PrivateKey
	Owner    ed25519.PrivateKey
	Genesis  state.Genesis
	Schedule rules.Schedule
}

func NewFixture(chain, name string, outputs int) Fixture {
	f := Fixture{Schedule: rules.DefaultSchedule()}
	f.Org = protocol.OrgConfig{Network: protocol.Digest("NETWORK", []byte(chain)), Org: protocol.Digest("ORG", []byte(name)), Epoch: 1}
	for i := range f.Keys {
		seed := protocol.Digest("LAB_MEMBER", []byte(chain), []byte(name), []byte{byte(i)})
		f.Keys[i] = ed25519.NewKeyFromSeed(seed[:])
		copy(f.Org.Members[i][:], f.Keys[i].Public().(ed25519.PublicKey))
	}
	seed := protocol.Digest("LAB_OWNER", []byte(chain), []byte(name))
	f.Owner = ed25519.NewKeyFromSeed(seed[:])
	descriptor := protocol.NewDescriptor(f.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: f.Org.Org}, f.Owner)
	f.Genesis.Network = f.Org.Network
	for i := 0; i < outputs; i++ {
		id := protocol.OutputID(protocol.Digest("LAB_OUTPUT", f.Org.Org[:], []byte(fmt.Sprint(i))))
		f.Genesis.Outputs = append(f.Genesis.Outputs, state.OriginOutput{ID: id, Output: protocol.Output{Asset: protocol.AssetCAL, Amount: 100, Recipient: descriptor}, Fact: protocol.Digest("LAB_FINAL", id[:])})
	}
	for _, kind := range []protocol.ResourceKind{protocol.ResourceFUEL, protocol.ResourceExecution, protocol.ResourceBytes, protocol.ResourcePolicy} {
		account := f.Org.Org
		if kind == protocol.ResourcePolicy {
			account = protocol.Digest("LAB_POLICY", f.Org.Org[:])
		}
		f.Genesis.Grants = append(f.Genesis.Grants, state.Grant{ID: protocol.Digest("LAB_GRANT", f.Org.Org[:], []byte{byte(kind)}), Organization: f.Org.Hash(), Key: protocol.ResourceKey{Kind: kind, Account: account, Version: 1}, Amount: 1_000_000_000, Subject: descriptor.Owner})
	}
	return f
}
func (f Fixture) Transaction(index int, nonce uint64) protocol.SignedTx {
	in := f.Genesis.Outputs[index]
	b := protocol.TxBody{Wire: 2, Version: 2, Network: f.Org.Network, Kind: protocol.FastTransfer, Subject: in.Output.Recipient.Owner, Certifier: f.Org.Org, Epoch: f.Org.Epoch, Config: f.Org.Hash(), Rules: f.Schedule.IDs(), Inputs: []protocol.Input{{Kind: protocol.FinalInput, Output: in.ID, Evidence: in.Fact}}, Outputs: []protocol.Output{in.Output}, Fee: protocol.FeeTerms{Source: protocol.OrgReserve, Account: f.Org.Org, Policy: f.Genesis.Grants[3].Key.Account, Version: 1, Maximum: 100}, Work: protocol.WorkLimit{Execution: 10000, Bytes: 1 << 20, Depth: 64, Ancestors: 256}}
	enc := new(protocol.Encoder)
	enc.U64(nonce)
	copy(b.Nonce[:], enc.Data())
	for _, g := range f.Genesis.Grants {
		b.Admission = append(b.Admission, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
	}
	return f.Sign(b)
}
func (f Fixture) Sign(b protocol.TxBody) protocol.SignedTx {
	b.Intent = b.IntentID()
	return protocol.SignedTx{Body: b, Auth: []protocol.OwnerAuth{protocol.SignOwner(b.ID(), f.Owner)}}
}
func (f Fixture) Member(index int, db store.Store) (*member.Member, error) {
	return member.New(member.Config{Organization: f.Org, Index: uint16(index), Key: f.Keys[index], Peers: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Workers: 1}, db, f.Genesis)
}
func (f Fixture) Certify(tx protocol.SignedTx, parents ...protocol.TXCer) (protocol.TXCer, error) {
	c := protocol.TXCer{Tx: tx}
	for i := 0; i < 3; i++ {
		db := store.NewMemory()
		m, e := f.Member(i, db)
		if e != nil {
			db.Close()
			return c, e
		}
		a, e := m.Approve(member.Request{Tx: tx, Parents: parents})
		db.Close()
		if e != nil {
			return c, e
		}
		c.Admission = a.Admission
		c.Effects = a.Effects
		c.QC.Fact = a.Fact
		c.QC.Votes = append(c.QC.Votes, a.Vote)
	}
	return c, c.Verify(f.Org)
}
