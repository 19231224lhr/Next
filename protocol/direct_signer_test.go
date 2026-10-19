package protocol

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
)

func TestDirectSignerC3PayerAndCanonicalBytes(t *testing.T) {
	for _, same := range []bool{true, false} {
		tx, owner, _, _ := c3TxFixture()
		payer := owner
		if !same {
			payer = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{3}, 32))
		}
		tx.Body.Fee = FeeTerms{Source: OwnerFinalUTXO, Maximum: 10, Inputs: []Input{{Kind: FinalInput, Output: OutputID(Digest("feeinput")), Evidence: Digest("feeevidence")}}, Refund: NewDescriptor(tx.Body.Network, Route{Kind: OrgRoute, Org: tx.Body.Certifier}, payer), C3: testC3()}
		c := &tx.Body.Fee.C3
		c.Sponsor = Hash{}
		copy(c.Payer[:], payer.Public().(ed25519.PublicKey))
		c.Resources = FeeResourcesFor(tx.Body)
		if err := tx.Body.validateVersion(DirectWireVersion, DirectVersion); err != nil {
			t.Fatal("invalid test setup", err)
		}
		tx.Auth = nil
		signer, err := NewDirectSigner(owner)
		if err != nil {
			t.Fatal(err)
		}
		got, id, raw, err := signer.Sign(DirectRequest{Tx: tx})
		if !same {
			if !errors.Is(err, ErrAuth) || raw != nil {
				t.Fatal("unsigned fee payer accepted", err)
			}
			continue
		}
		tx.Auth = []OwnerAuth{SignOwner(tx.ID(), owner)}
		want, e := (DirectRequest{Tx: tx}).MarshalBinary()
		if e != nil || err != nil || !bytes.Equal(raw, want) || id != tx.ID() || got.VerifyAuth() != nil {
			t.Fatal("C3 differs", e, err)
		}
	}
}
