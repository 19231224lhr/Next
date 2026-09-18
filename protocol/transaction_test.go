package protocol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func sample(t *testing.T) (TxBody, ed25519.PrivateKey, OrgConfig) {
	t.Helper()
	_, owner, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	net := Digest("NETWORK", []byte("test"))
	org := Digest("ORG", []byte("a"))
	cfg := OrgConfig{Network: net, Org: org, Epoch: 1}
	for i := range cfg.Members {
		p, _, _ := ed25519.GenerateKey(rand.Reader)
		copy(cfg.Members[i][:], p)
	}
	desc := NewDescriptor(net, Route{Kind: OrgRoute, Org: org}, owner)
	tx := TxBody{Wire: 2, Version: 2, Network: net, Kind: FastTransfer, Subject: desc.Owner, Nonce: Nonce{1}, Certifier: org, Epoch: 1, Config: cfg.Hash(), Rules: RuleIDs{Digest("fee", nil), Digest("work", nil), Digest("account", nil)}, Inputs: []Input{{Kind: FinalInput, Output: OutputID(Digest("input", nil)), Evidence: Digest("final", nil)}}, Outputs: []Output{{Asset: AssetCAL, Amount: 100, Recipient: desc}}, Fee: FeeTerms{Source: OrgReserve, Account: org, Version: 1, Maximum: 100, Policy: Digest("policy", nil)}, Work: WorkLimit{Execution: 100, Bytes: 4096, Depth: 64, Ancestors: 256}}
	tx.Intent = tx.IntentID()
	return tx, owner, cfg
}
func TestC02CanonicalTxAndAuthorization(t *testing.T) {
	tx, key, cfg := sample(t)
	b, e := tx.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := DecodeTx(b)
	if e != nil {
		t.Fatal(e)
	}
	b2, _ := decoded.MarshalBinary()
	if !bytes.Equal(b, b2) {
		t.Fatal("noncanonical round trip")
	}
	auth := SignOwner(tx.ID(), key)
	if e := VerifyOwner(tx.ID(), auth); e != nil {
		t.Fatal(e)
	}
	tx.Outputs[0].Amount++
	if VerifyOwner(tx.ID(), auth) == nil {
		t.Fatal("output not signed")
	}
	tx.Outputs[0].Amount--
	tx.Network[0] ^= 1
	if VerifyOwner(tx.ID(), auth) == nil {
		t.Fatal("network not bound")
	}
	if cfg.Validate() != nil {
		t.Fatal("configuration invalid")
	}
	cfg.Members[1] = cfg.Members[0]
	if cfg.Validate() == nil {
		t.Fatal("duplicate member")
	}
}
func TestC01QuorumIdentityAndDistinctVotes(t *testing.T) {
	tx, _, cfg := sample(t)
	keys := make([]ed25519.PrivateKey, 4)
	for i := range keys {
		p, k, _ := ed25519.GenerateKey(rand.Reader)
		keys[i] = k
		copy(cfg.Members[i][:], p)
	}
	tx.Config = cfg.Hash()
	fact := SpendID(tx.ID(), AdmissionVector{{Key: ResourceKey{Kind: ResourceFUEL, Account: cfg.Org, Version: 1}, Cap: 100}}, Digest("effects", nil), tx.Rules)
	qc := SpendQC{Fact: fact}
	for i := 0; i < 3; i++ {
		qc.Votes = append(qc.Votes, SignSpend(fact, uint16(i), keys[i]))
	}
	if e := VerifyQC(qc, cfg); e != nil {
		t.Fatal(e)
	}
	qc.Votes[2] = qc.Votes[1]
	if VerifyQC(qc, cfg) == nil {
		t.Fatal("duplicate votes counted")
	}
	qc.Votes[2] = SignSpend(fact, 3, keys[3])
	if e := VerifyQC(qc, cfg); e != nil {
		t.Fatal(e)
	}
	qc.Fact[0] ^= 1
	if VerifyQC(qc, cfg) == nil {
		t.Fatal("tampered fact")
	}
}
func TestTransactionRejectsNoncanonicalAndUnsupported(t *testing.T) {
	tx, _, _ := sample(t)
	raw, _ := tx.MarshalBinary()
	if _, e := DecodeTx(append(raw, 0)); e == nil {
		t.Fatal("trailing bytes")
	}
	tx.Inputs = append(tx.Inputs, tx.Inputs[0])
	if _, e := tx.MarshalBinary(); e == nil {
		t.Fatal("duplicate input")
	}
	tx, _, _ = sample(t)
	tx.Wire = 1
	if _, e := tx.MarshalBinary(); e == nil {
		t.Fatal("legacy wire")
	}
	tx, _, _ = sample(t)
	tx.Outputs[0].Recipient.Route.Org[0] ^= 1
	if tx.Outputs[0].Recipient.Verify(tx.Network) == nil {
		t.Fatal("route forgery")
	}
}
func FuzzTransaction(f *testing.F) {
	f.Add([]byte{0, 2})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > MaxTxBytes {
			return
		}
		tx, e := DecodeTx(b)
		if e == nil {
			encoded, e := tx.MarshalBinary()
			if e != nil || !bytes.Equal(encoded, b) {
				t.Fatal("noncanonical accepted")
			}
		}
	})
}
