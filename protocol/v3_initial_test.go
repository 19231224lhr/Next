package protocol

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"

	"utxo/crypto/chameleon"
)

func initialTxFixture(t testing.TB, c3 bool) (FastTx, *chameleon.Public) {
	t.Helper()
	raw, err := os.ReadFile("../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := chameleon.NewPublic(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	tx, owner, _, _ := c3TxFixture()
	if c3 {
		tx.Body.Fee.C3 = testC3()
		tx.Body.Fee.C3.Resources = FeeResourcesFor(tx.Body)
	}
	tx, err = NewFastTx(tx.Body, tx.Claims, pub)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []OwnerAuth{SignOwner(tx.ID(), owner)}
	return tx, pub
}

func TestMarshalInitialMatchesAuthenticatedEncoding(t *testing.T) {
	for _, c3 := range []bool{false, true} {
		tx, key := initialTxFixture(t, c3)
		if err := tx.VerifyInitial(key); err != nil {
			t.Fatal(err)
		}
		want, err := tx.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		got, err := tx.MarshalInitial(key)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("c3=%v: encoding changed", c3)
		}
		decoded, err := DecodeFastTx(got)
		if err != nil || decoded.VerifyInitial(key) != nil {
			t.Fatalf("c3=%v: initial wire roundtrip failed: %v", c3, err)
		}
		decoded.Funding[0].Opening[0] ^= 1
		if !bytes.Equal(got, want) {
			t.Fatal("decoded funding aliases returned wire")
		}
		tx.Body.Outputs[0].Amount++
		if !bytes.Equal(got, want) {
			t.Fatal("returned wire aliases transaction")
		}
		if _, err := tx.MarshalInitial(key); err == nil {
			t.Fatal("changed body accepted")
		}
	}
}

func TestMarshalInitialRejectsMutatedInputs(t *testing.T) {
	for _, mutate := range []func(*FastTx){
		func(tx *FastTx) { tx.Auth[0].Signature[0] ^= 1 },
		func(tx *FastTx) { tx.Funding[0].Opening[0] ^= 1 },
		func(tx *FastTx) { tx.Funding[0].Ref = Digest("other") },
		func(tx *FastTx) { tx.Funding[0].Kind = ReserveFunding },
		func(tx *FastTx) { tx.Claims[0].Output.Amount++ },
	} {
		tx, key := initialTxFixture(t, true)
		if _, err := tx.MarshalInitial(key); err != nil {
			t.Fatal(err)
		}
		mutate(&tx)
		if raw, err := tx.MarshalInitial(key); err == nil || raw != nil {
			t.Fatal("mutated input accepted")
		}
	}
}

func BenchmarkMarshalInitial(b *testing.B) {
	tx, key := initialTxFixture(b, true)
	b.Run("VerifyThenMarshal", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := tx.VerifyInitial(key); err != nil {
				b.Fatal(err)
			}
			if _, err := tx.MarshalBinary(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("MarshalInitial", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := tx.MarshalInitial(key); err != nil {
				b.Fatal(err)
			}
		}
	})
}
