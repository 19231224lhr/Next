package protocol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"

	"utxo/crypto/chameleon"
)

func TestV3CertificateIsDetachedAndBindsOutput(t *testing.T) {
	net := Digest("v3-test-network")
	var cfg OrgConfig
	cfg.Network, cfg.Org, cfg.Epoch = net, Digest("org"), 1
	var keys [4]ed25519.PrivateKey
	for i := range keys {
		pub, k, _ := ed25519.GenerateKey(rand.Reader)
		keys[i] = k
		copy(cfg.Members[i][:], pub)
	}
	// The compact certificate needs no transaction body or ancestry.
	_, owner, _ := ed25519.GenerateKey(rand.Reader)
	desc := NewDescriptor(net, Route{Kind: OrgRoute, Org: cfg.Org}, owner)
	out := Output{Asset: AssetCAL, Amount: 100, Recipient: desc}
	s := OutputSummary{Network: net, Tx: TxID(Digest("transaction")), Issuer: cfg.Org, Config: cfg.Hash(), Epoch: 1,
		Rules:     RuleIDs{Fee: Digest("fee"), Work: Digest("work"), Accounting: Digest("accounting")},
		Outputs:   []OutputCommitment{{Digest: OutputDigest(out), Amount: 100}},
		Admission: AdmissionVector{{Key: ResourceKey{Kind: ResourceCAL, Account: cfg.Org, Version: 1}, Cap: 100}},
		Grants:    []Hash{Digest("grant")}}
	c := OutputCertificate{Summary: s}
	c.QC.Fact = s.Fact()
	for i := 0; i < 3; i++ {
		c.QC.Votes = append(c.QC.Votes, SignSpend(c.QC.Fact, uint16(i), keys[i]))
	}
	if err := c.VerifyOutput(cfg, 0, out); err != nil {
		t.Fatal(err)
	}
	wire, err := c.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeOutputCertificate(wire)
	if err != nil {
		t.Fatal(err)
	}
	if err := decoded.VerifyOutput(cfg, 0, out); err != nil {
		t.Fatal(err)
	}
	changed := out
	changed.Amount++
	if err := decoded.VerifyOutput(cfg, 0, changed); err == nil {
		t.Fatal("unbound output amount")
	}
	decoded.Summary.Outputs[0].Amount++
	if err := decoded.Verify(cfg); err == nil {
		t.Fatal("summary modification accepted")
	}
	if bytes.Contains(wire, owner) {
		t.Fatal("certificate contains owner secret")
	}
}

func TestV3TransactionStableIdentity(t *testing.T) {
	b, err := os.ReadFile("../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	pemKey, _ := pem.Decode(b)
	key, err := x509.ParsePKCS1PrivateKey(pemKey.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := chameleon.NewPublic(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	_, owner, _ := ed25519.GenerateKey(rand.Reader)
	net, org := Digest("v3-network"), Digest("v3-org")
	desc := NewDescriptor(net, Route{Kind: OrgRoute, Org: org}, owner)
	base := TxBody{Wire: 4, Version: 4, Network: net, Kind: FastTransfer, Certifier: org, Config: Digest("config"), Epoch: 1,
		Rules:   RuleIDs{Fee: Digest("fee"), Work: Digest("work"), Accounting: Digest("account")},
		Inputs:  []Input{{Kind: CertificateInput, Output: OutputID(Digest("input")), Evidence: Digest("evidence")}},
		Outputs: []Output{{Asset: AssetCAL, Amount: 100, Recipient: desc}},
		Fee:     FeeTerms{Source: OrgReserve, Account: org, Policy: Digest("policy"), Version: 1, Maximum: 100},
		Work:    WorkLimit{Execution: 1000, Bytes: 100000, Depth: 1, Ancestors: 1}}
	copy(base.Subject[:], owner.Public().(ed25519.PublicKey))
	base.Intent = base.IntentID()
	tx, err := NewFastTx(base, []InputClaim{{Output: base.Outputs[0]}}, pub)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []OwnerAuth{SignOwner(tx.ID(), owner)}
	if err := tx.VerifyInitial(pub); err != nil {
		t.Fatal(err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	copyTx, err := DecodeFastTx(raw)
	if err != nil {
		t.Fatal(err)
	}
	if copyTx.ID() != tx.ID() || copyTx.VerifyInitial(pub) != nil {
		t.Fatal("wire roundtrip changed semantics")
	}
	copyTx.Funding[0].Ref = Hash(Digest("reserve-debit"))
	if copyTx.ID() != tx.ID() {
		t.Fatal("mutable ref entered transaction identity")
	}
	if copyTx.VerifyInitial(pub) == nil {
		t.Fatal("unauthorized initial funding accepted")
	}
	copyTx.Claims[0].Output.Amount++
	if copyTx.ID() == tx.ID() {
		t.Fatal("input description not bound")
	}
}
