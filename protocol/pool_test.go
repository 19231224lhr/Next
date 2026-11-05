package protocol

import (
	"crypto/ed25519"
	"testing"
)

func TestPoolCertificateDomainsAndQuorum(t *testing.T) {
	org := OrgConfig{Network: Digest("net"), Org: Digest("org"), Epoch: 1}
	var keys [4]ed25519.PrivateKey
	for i := range keys {
		s := Digest("key", []byte{byte(i)})
		keys[i] = ed25519.NewKeyFromSeed(s[:])
		copy(org.Members[i][:], keys[i].Public().(ed25519.PublicKey))
	}
	owner := keys[0]
	input := OutputID(Digest("input"))
	a := PoolAction{Kind: PoolDeposit, Network: org.Network, Config: org.Hash(), Pool: PoolIdentity(org.Network, org.Hash(), input), Input: input, Evidence: Digest("evidence"), Amount: 100, To: NewDescriptor(org.Network, Route{Kind: OrgRoute, Org: org.Org}, owner)}
	a.Sign(owner)
	c := PoolCertificate{Action: a}
	for i := 0; i < 3; i++ {
		c.Votes = append(c.Votes, SignPool(a, uint16(i), keys[i]))
	}
	if e := c.Verify(org); e != nil {
		t.Fatal(e)
	}
	b, e := c.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	round, e := DecodePoolCertificate(b)
	if e != nil || round.Verify(org) != nil {
		t.Fatal("roundtrip", e)
	}
	if _, e = DecodeDirectSubmission(b); e == nil {
		t.Fatal("pool is a direct submission")
	}
	bad := c
	bad.Votes = append([]PoolVote{}, c.Votes...)
	bad.Votes[2] = bad.Votes[1]
	if bad.Verify(org) == nil {
		t.Fatal("duplicate signer")
	}
	bad = c
	bad.Action.Amount++
	if bad.Verify(org) == nil {
		t.Fatal("amount tamper")
	}
	bad = c
	bad.Votes = append([]PoolVote{}, c.Votes...)
	for i := range bad.Votes {
		v := SignSpend(SpendFactID(a.ID()), uint16(i), keys[i])
		bad.Votes[i] = PoolVote{Member: v.Member, Signature: v.Signature}
	}
	if bad.Verify(org) == nil {
		t.Fatal("ordinary signatures crossed protocol domain")
	}
	foreign := org
	foreign.Epoch++
	if c.Verify(foreign) == nil {
		t.Fatal("foreign configuration")
	}
	if _, e = DecodePoolCertificate(append(b, []byte("{}")...)); e == nil {
		t.Fatal("trailing JSON")
	}
}
