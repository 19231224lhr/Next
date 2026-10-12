package protocol

import (
	"crypto/ed25519"
	"testing"
)

func verifiedOutputsFixture() (OrgConfig, OutputCertificate, Output) {
	net := Digest("verified-outputs-network")
	cfg := OrgConfig{Network: net, Org: Digest("verified-outputs-org"), Epoch: 1}
	var keys [4]ed25519.PrivateKey
	for i := range keys {
		seed := Digest("verified-outputs-member", []byte{byte(i)})
		keys[i] = ed25519.NewKeyFromSeed(seed[:])
		copy(cfg.Members[i][:], keys[i].Public().(ed25519.PublicKey))
	}
	out := Output{Asset: AssetCAL, Amount: 100, Recipient: NewDescriptor(net, Route{Kind: OrgRoute, Org: cfg.Org}, keys[0])}
	s := OutputSummary{Network: net, Tx: TxID(Digest("verified-outputs-tx")), Issuer: cfg.Org, Config: cfg.Hash(), Epoch: 1,
		Rules:     RuleIDs{Fee: Digest("fee"), Work: Digest("work"), Accounting: Digest("accounting")},
		Outputs:   []OutputCommitment{{Digest: OutputDigest(out), Amount: out.Amount}},
		Admission: AdmissionVector{{Key: ResourceKey{Kind: ResourceCAL, Account: cfg.Org, Version: 1}, Cap: out.Amount}},
		Grants:    []Hash{Digest("grant")}}
	c := OutputCertificate{Summary: s, QC: SpendQC{Fact: s.Fact()}}
	for i := 0; i < 3; i++ {
		c.QC.Votes = append(c.QC.Votes, SignSpend(c.QC.Fact, uint16(i), keys[i]))
	}
	return cfg, c, out
}

func TestVerifiedOutputsRejectInvalidCertificate(t *testing.T) {
	for _, name := range []string{"signature", "quorum", "config", "summary"} {
		t.Run(name, func(t *testing.T) {
			cfg, c, out := verifiedOutputsFixture()
			switch name {
			case "signature":
				c.QC.Votes[0].Signature[0] ^= 1
			case "quorum":
				c.QC.Votes = c.QC.Votes[:2]
			case "config":
				cfg.Epoch++
			case "summary":
				c.Summary.Outputs[0].Digest[0] ^= 1
			}
			verified, err := c.VerifyOutputs(cfg)
			if err == nil {
				t.Fatal("invalid certificate accepted")
			}
			if verified.VerifyOutput(0, out) == nil {
				t.Fatal("failed verification produced usable outputs")
			}
		})
	}
}

func TestVerifiedOutputsBindAndOwnCommitments(t *testing.T) {
	cfg, c, out := verifiedOutputsFixture()
	verified, err := c.VerifyOutputs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := verified.VerifyOutput(0, out); err != nil {
		t.Fatal(err)
	}
	if (VerifiedOutputs{}).VerifyOutput(0, out) == nil {
		t.Fatal("zero value accepted")
	}
	for _, name := range []string{"amount", "recipient", "asset", "index", "large-index"} {
		t.Run(name, func(t *testing.T) {
			changed, index := out, uint32(0)
			switch name {
			case "amount":
				changed.Amount++
			case "recipient":
				changed.Recipient.Owner[0] ^= 1
			case "asset":
				changed.Asset = AssetFUEL
			case "index":
				index = 1
			case "large-index":
				index = ^uint32(0)
			}
			if verified.VerifyOutput(index, changed) == nil {
				t.Fatal("unbound output accepted")
			}
		})
	}
	changed := out
	changed.Amount++
	c.Summary.Outputs[0] = OutputCommitment{Digest: OutputDigest(changed), Amount: changed.Amount}
	if verified.VerifyOutput(0, changed) == nil {
		t.Fatal("caller mutation changed verified commitments")
	}
	if err := verified.VerifyOutput(0, out); err != nil {
		t.Fatal("caller mutation invalidated snapshot:", err)
	}
}

func BenchmarkParentOutputVerification(b *testing.B) {
	cfg, c, out := verifiedOutputsFixture()
	b.Run("previous", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := c.Verify(cfg); err != nil {
				b.Fatal(err)
			}
			if err := c.VerifyOutput(cfg, 0, out); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("once", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			verified, err := c.VerifyOutputs(cfg)
			if err != nil {
				b.Fatal(err)
			}
			if err := verified.VerifyOutput(0, out); err != nil {
				b.Fatal(err)
			}
		}
	})
}
