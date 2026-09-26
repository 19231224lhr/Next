package chameleon

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"testing"

	tss "github.com/cloudflare/circl/tss/rsa"
)

func TestPublicCommitment(t *testing.T) {
	// Public arithmetic only; this modulus is not used for threshold operations.
	n := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 2048), big.NewInt(159))
	p, err := NewPublic(&rsa.PublicKey{N: n, E: 65537})
	if err != nil {
		t.Fatal(err)
	}
	c, r, err := p.Commit([]byte("context"), []byte("message"))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Verify([]byte("context"), []byte("message"), c, r) {
		t.Fatal("public opening failed")
	}
	if p.Verify([]byte("context"), []byte("other"), c, r) {
		t.Fatal("unmodified opening accepted wrong message")
	}
}

// Published collisions disclose a reusable ratio root, not a fresh business
// authorization. This positive property prevents stronger security claims.
func TestPublishedAdaptationReuse(t *testing.T) {
	p, signers := fixture(t)
	ctx, old, next := []byte("same-context"), []byte("old"), []byte("new")
	c, r, err := p.Commit(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	var shares []Contribution
	for i := 0; i < 3; i++ {
		s, err := signers[i].Adapt(ctx, old, next, c, r)
		if err != nil {
			t.Fatal(err)
		}
		shares = append(shares, s)
	}
	r2, err := p.Combine(ctx, old, next, c, r, shares)
	if err != nil {
		t.Fatal(err)
	}
	z := new(big.Int).ModInverse(new(big.Int).SetBytes(r[:]), p.key.N)
	z.Mul(z, new(big.Int).SetBytes(r2[:])).Mod(z, p.key.N)
	otherC, otherR, err := p.Commit(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	x := new(big.Int).Mul(new(big.Int).SetBytes(otherR[:]), z)
	x.Mod(x, p.key.N)
	var adapted Opening
	x.FillBytes(adapted[:])
	if !p.Verify(ctx, next, otherC, adapted) {
		t.Fatal("public ratio root should transfer within the same context")
	}
	if p.Verify([]byte("different-context"), next, otherC, adapted) {
		t.Fatal("opening transferred across contexts")
	}
}

func fixture(t testing.TB) (*Public, []Signer) {
	t.Helper()
	b, err := os.ReadFile("testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := pem.Decode(b)
	key, err := x509.ParsePKCS1PrivateKey(p.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := NewPublic(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	shares, err := tss.Deal(rand.Reader, 4, 3, key, true)
	if err != nil {
		t.Fatal(err)
	}
	signers := make([]Signer, 4)
	for i := range shares {
		signers[i] = Signer{public: pub, share: shares[i]}
	}
	return pub, signers
}

func TestEveryQuorumAndRepeatedPublicRepair(t *testing.T) {
	p, signers := fixture(t)
	ctx := []byte("network/height/part")
	old := []byte("source A")
	c, r, err := p.Commit(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 4; round++ {
		next := []byte{byte(round), 7, 3}
		var parts []Contribution
		for i := range signers {
			s, err := signers[i].Adapt(ctx, old, next, c, r)
			if err != nil {
				t.Fatal(err)
			}
			parts = append(parts, s)
		}
		var repaired Opening
		for missing := range parts {
			quorum := append([]Contribution{}, parts[:missing]...)
			quorum = append(quorum, parts[missing+1:]...)
			r2, err := p.Combine(ctx, old, next, c, r, quorum)
			if err != nil {
				t.Fatalf("round=%d absent=%d: %v", round, missing+1, err)
			}
			if !p.Verify(ctx, next, c, r2) {
				t.Fatal("repaired commitment invalid")
			}
			if missing > 0 && r2 != repaired {
				t.Fatal("quorums disagree")
			}
			repaired = r2
		}
		if p.Verify([]byte("other network"), next, c, repaired) {
			t.Fatal("context not bound")
		}
		if p.Verify(ctx, old, c, repaired) {
			t.Fatal("old message accepted under new opening")
		}
		old, r = next, repaired
	}
}

func TestBadOrInsufficientShares(t *testing.T) {
	p, signers := fixture(t)
	ctx, old, next := []byte("input/0"), []byte("original"), []byte("reserve")
	c, r, _ := p.Commit(ctx, old)
	var parts []Contribution
	for i := range signers {
		s, err := signers[i].Adapt(ctx, old, next, c, r)
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, s)
	}
	if _, err := p.Combine(ctx, old, next, c, r, parts[:2]); err == nil {
		t.Fatal("two shares accepted")
	}
	if _, err := p.Combine(ctx, old, next, c, r, []Contribution{parts[0], parts[0], parts[1]}); err == nil {
		t.Fatal("duplicate share accepted")
	}
	bad, err := signers[0].Adapt(ctx, old, []byte("other target"), c, r)
	if err != nil {
		t.Fatal(err)
	}
	parts[0] = bad
	r2, err := p.Combine(ctx, old, next, c, r, parts)
	if err != nil || !p.Verify(ctx, next, c, r2) {
		t.Fatalf("one faulty member blocked honest quorum: %v", err)
	}
	if _, err := signers[1].Adapt(ctx, []byte("wrong original"), next, c, r); err == nil {
		t.Fatal("invalid original accepted")
	}
	if p.Verify(ctx, old, c, Opening{}) {
		t.Fatal("zero opening accepted")
	}
	if bytes.Equal(r[:], r2[:]) {
		t.Fatal("opening did not change")
	}
}

func BenchmarkCommit(b *testing.B) {
	p, _ := fixture(b)
	data := bytes.Repeat([]byte{42}, 65536)
	b.ResetTimer()
	for b.Loop() {
		if _, _, err := p.Commit([]byte("part"), data); err != nil {
			b.Fatal(err)
		}
	}
}
