package chameleon

import (
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
)

func commitmentPublic(t testing.TB) *Public {
	t.Helper()
	raw, err := os.ReadFile("testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPublic(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCommitMatchesPublicFormula(t *testing.T) {
	p := commitmentPublic(t)
	for i := 0; i < 32; i++ {
		ctx, message := []byte{byte(i), 1}, []byte{byte(i), 2}
		c, r, err := p.Commit(ctx, message)
		if err != nil {
			t.Fatal(err)
		}
		x := new(big.Int).SetBytes(r[:])
		if !p.unit(x) {
			t.Fatal("commit returned an invalid opening")
		}
		want := new(big.Int).Exp(x, big.NewInt(int64(p.key.E)), p.key.N)
		want.Mul(want, p.representative(ctx, message)).Mod(want, p.key.N)
		if new(big.Int).SetBytes(c[:]).Cmp(want) != 0 || !p.Verify(ctx, message, c, r) {
			t.Fatal("commitment formula changed")
		}
		if p.Verify([]byte("wrong context"), message, c, r) || p.Verify(ctx, []byte("wrong message"), c, r) {
			t.Fatal("commitment binding lost")
		}
	}
}

func BenchmarkCommitPreparation(b *testing.B) {
	p := commitmentPublic(b)
	ctx, message := []byte("input context"), []byte("source reference")
	b.Run("legacy_double_unit_check", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for {
				x, err := rand.Int(rand.Reader, p.key.N)
				if err != nil {
					b.Fatal(err)
				}
				if !p.unit(x) {
					continue
				}
				var r Opening
				x.FillBytes(r[:])
				if _, ok := p.value(ctx, message, r); !ok {
					b.Fatal("bad opening")
				}
				break
			}
		}
	})
	b.Run("current", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, _, err := p.Commit(ctx, message); err != nil {
				b.Fatal(err)
			}
		}
	})
}
