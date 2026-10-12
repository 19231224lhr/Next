package protocol

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestDirectV5RejectsLegacyV4Body(t *testing.T) {
	body, _, _ := sample(t)
	body.Wire, body.Version = 4, 4
	if _, err := NewFastTx(body, []InputClaim{{Output: body.Outputs[0]}}, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("legacy v4 body must be rejected before construction: %v", err)
	}
}

func TestDirectV5RejectsLegacyV4Envelope(t *testing.T) {
	raw, err := encodeDirectEnvelope([]byte("fixed"), []byte("mutable"))
	if err != nil {
		t.Fatal(err)
	}
	copy(raw[:8], []byte{'U', 'T', 'X', 'O', '4', 'C', 'H', 0})
	if _, _, err := decodeDirectEnvelope(raw); !errors.Is(err, ErrEncoding) {
		t.Fatalf("legacy v4 direct envelope accepted: %v", err)
	}
}

func TestDirectV5RejectsLegacyV4CertificateSummary(t *testing.T) {
	body, _, cfg := sample(t)
	summary := OutputSummary{Network: cfg.Network, Tx: body.ID(), Issuer: cfg.Org, Config: cfg.Hash(), Epoch: cfg.Epoch, Rules: body.Rules, Outputs: []OutputCommitment{{Digest: OutputDigest(body.Outputs[0]), Amount: body.Outputs[0].Amount}}, Admission: AdmissionVector{{Key: ResourceKey{Kind: ResourceCAL, Account: cfg.Org, Version: 1}, Cap: body.Outputs[0].Amount}}, Grants: []Hash{Digest("v5-test-grant")}}
	c := OutputCertificate{Summary: summary, QC: SpendQC{Fact: summary.Fact(), Votes: make([]SpendVote, 3)}}
	raw, err := c.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeOutputCertificate(raw); err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint64(raw[6:14], 4)
	if _, err := DecodeOutputCertificate(raw); !errors.Is(err, ErrEncoding) {
		t.Fatalf("legacy v4 certificate summary accepted: %v", err)
	}
}

func TestDirectV5TransactionIdentityDomain(t *testing.T) {
	body, _, _ := sample(t)
	tx := FastTx{Body: body}
	e := new(Encoder)
	e.Bytes(tx.core())
	if got, want := tx.ID(), TxID(Digest("TX_V5", e.Data())); got != want {
		t.Fatalf("direct transaction uses an old identity domain: got %x, want %x", got, want)
	}
}
