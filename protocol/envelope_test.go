package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestCertificateEnvelopeAndEffects(t *testing.T) {
	tx, owner, cfg := sample(t)
	keys := make([]ed25519.PrivateKey, 4)
	for i := range keys {
		p, k, _ := ed25519.GenerateKey(rand.Reader)
		keys[i] = k
		copy(cfg.Members[i][:], p)
	}
	tx.Config = cfg.Hash()
	signed := SignedTx{Body: tx, Auth: []OwnerAuth{SignOwner(tx.ID(), owner)}}
	effects := EffectsFor(tx, 1, 1)
	vector := AdmissionVector{{Key: ResourceKey{Kind: ResourceFUEL, Account: tx.Certifier, Version: 1}, Cap: 100}}
	c := TXCer{Tx: signed, Admission: vector, Effects: effects}
	c.QC.Fact = SpendID(tx.ID(), vector, effects.Hash(), tx.Rules)
	for i := 0; i < 3; i++ {
		c.QC.Votes = append(c.QC.Votes, SignSpend(c.QC.Fact, uint16(i), keys[i]))
	}
	if e := c.Verify(cfg); e != nil {
		t.Fatal(e)
	}
	b, e := c.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := DecodeCertificate(b)
	if e != nil || decoded.Verify(cfg) != nil {
		t.Fatalf("roundtrip %v", e)
	}
	decoded.Admission[0].Cap++
	if decoded.Verify(cfg) == nil {
		t.Fatal("changed vector")
	}
	decoded, _ = DecodeCertificate(b)
	decoded.Effects.Outputs[0][0] ^= 1
	if decoded.Verify(cfg) == nil {
		t.Fatal("changed output")
	}
	cfg.Epoch++
	if c.Verify(cfg) == nil {
		t.Fatal("changed config epoch")
	}
}
func TestSignedTxBoundedEnvelope(t *testing.T) {
	tx, key, _ := sample(t)
	s := SignedTx{Body: tx, Auth: []OwnerAuth{SignOwner(tx.ID(), key)}}
	b, e := s.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	d, e := DecodeSignedTx(b)
	if e != nil || d.VerifyAuth() != nil {
		t.Fatal("valid")
	}
	s.Auth = append(s.Auth, s.Auth[0])
	if _, e = s.MarshalBinary(); e == nil {
		t.Fatal("duplicate owner")
	}
	if _, e = DecodeSignedTx(append(b, 0)); e == nil {
		t.Fatal("trailing")
	}
}
