// Package chameleon implements the experimental RSA-FDH chameleon commitment.
// It is a cryptographic primitive; callers must separately authorize repairs.
package chameleon

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	tss "github.com/cloudflare/circl/tss/rsa"
	"golang.org/x/crypto/sha3"
	"io"
	"math/big"
)

const Size = 256

type Commitment [Size]byte
type Opening [Size]byte
type Public struct{ key rsa.PublicKey }
type Signer struct {
	public *Public
	share  tss.KeyShare
}
type Contribution struct{ share tss.SignShare }

var ErrInvalid = errors.New("invalid chameleon commitment or contribution")

func NewPublic(key *rsa.PublicKey) (*Public, error) {
	if key == nil || key.N == nil || key.N.BitLen() != Size*8 || key.N.Bit(0) != 1 || key.E != 65537 {
		return nil, ErrInvalid
	}
	return &Public{key: rsa.PublicKey{N: new(big.Int).Set(key.N), E: key.E}}, nil
}

func NewSigner(p *Public, share tss.KeyShare) (Signer, error) {
	if p == nil || share.Players != 4 || share.Threshold != 3 || share.Index < 1 || share.Index > 4 {
		return Signer{}, ErrInvalid
	}
	return Signer{public: p, share: share}, nil
}

// Digest evaluates a public opening. It does not authorize the message.
func (p *Public) Digest(context, message []byte, r Opening) (Commitment, error) {
	c, ok := p.value(context, message, r)
	if !ok {
		return Commitment{}, ErrInvalid
	}
	return c, nil
}

// KeyID binds the modulus; signers never take an RSA modulus from a request.
func (p *Public) KeyID() [32]byte { return sha256.Sum256(p.key.N.Bytes()) }

// representative is RSA full-domain hashing, not a short digest treated as an
// integer. Framing binds the key, context and complete message independently.
func (p *Public) representative(context, message []byte) *big.Int {
	h := sha3.NewShake256()
	h.Write([]byte("UTXO_CH_RSA_FDH_V3"))
	h.Write(p.key.N.Bytes())
	for _, v := range [][]byte{context, message} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(v)))
		h.Write(size[:])
		h.Write(v)
	}
	var b [Size]byte
	for {
		io.ReadFull(h, b[:]) // SHAKE is an infallible extendable output function.
		x := new(big.Int).SetBytes(b[:])
		if p.unit(x) {
			return x
		}
	}
}

func (p *Public) unit(x *big.Int) bool {
	return x.Sign() > 0 && x.Cmp(p.key.N) < 0 && new(big.Int).GCD(nil, nil, x, p.key.N).Cmp(big.NewInt(1)) == 0
}

func (p *Public) value(context, message []byte, r Opening) (Commitment, bool) {
	x := new(big.Int).SetBytes(r[:])
	if !p.unit(x) {
		return Commitment{}, false
	}
	x.Exp(x, big.NewInt(int64(p.key.E)), p.key.N)
	x.Mul(x, p.representative(context, message)).Mod(x, p.key.N)
	var c Commitment
	x.FillBytes(c[:])
	return c, true
}

func (p *Public) Commit(context, message []byte) (Commitment, Opening, error) {
	var r Opening
	for {
		x, err := rand.Int(rand.Reader, p.key.N)
		if err != nil {
			return Commitment{}, r, err
		}
		if !p.unit(x) {
			continue
		}
		x.FillBytes(r[:])
		c, _ := p.value(context, message, r)
		return c, r, nil
	}
}

func (p *Public) Verify(context, message []byte, c Commitment, r Opening) bool {
	got, ok := p.value(context, message, r)
	return ok && got == c
}

func (p *Public) ratio(context, old, next []byte) []byte {
	x := p.representative(context, old)
	y := p.representative(context, next)
	y.ModInverse(y, p.key.N)
	x.Mul(x, y).Mod(x, p.key.N)
	return x.FillBytes(make([]byte, Size))
}

// Adapt computes one RSA-root share, never the private exponent. The enclosing
// committee service must validate the repair policy before calling it. This is
// raw group arithmetic for CH, not the library's padded RSA signature API.
func (s *Signer) Adapt(context, old, next []byte, c Commitment, r Opening) (Contribution, error) {
	if !s.public.Verify(context, old, c, r) {
		return Contribution{}, ErrInvalid
	}
	share, err := s.share.Sign(rand.Reader, &s.public.key, s.public.ratio(context, old, next), true)
	return Contribution{share: share}, err
}

func (p *Public) Combine(context, old, next []byte, c Commitment, r Opening, shares []Contribution) (Opening, error) {
	if len(shares) < 3 || len(shares) > 4 || !p.Verify(context, old, c, r) {
		return Opening{}, ErrInvalid
	}
	seen := uint(0)
	for _, s := range shares {
		i := s.share.Index
		if i < 1 || i > 4 || s.share.Players != 4 || s.share.Threshold != 3 || seen&(1<<i) != 0 {
			return Opening{}, ErrInvalid
		}
		seen |= 1 << i
	}
	q := p.ratio(context, old, next)
	// Four fixed members => at most four triples. This avoids a new share-proof
	// protocol while tolerating one authenticated but incorrect contribution.
	for i := 0; i < len(shares)-2; i++ {
		for j := i + 1; j < len(shares)-1; j++ {
			for k := j + 1; k < len(shares); k++ {
				root, err := tss.CombineSignShares(&p.key, 4, 3, []tss.SignShare{shares[i].share, shares[j].share, shares[k].share}, q)
				if err != nil {
					continue
				}
				x := new(big.Int).SetBytes(root)
				x.Mul(x, new(big.Int).SetBytes(r[:])).Mod(x, p.key.N)
				var result Opening
				x.FillBytes(result[:])
				if p.Verify(context, next, c, result) {
					return result, nil
				}
			}
		}
	}
	return Opening{}, ErrInvalid
}
