package protocol

import (
	"crypto/ed25519"
)

type SpendVote struct {
	Member    uint16
	Signature Signature
}
type SpendQC struct {
	Fact  SpendFactID
	Votes []SpendVote
}

func SpendID(tx TxID, v AdmissionVector, effects Hash, rules RuleIDs) SpendFactID {
	e := new(Encoder)
	rules.encode(e)
	return SpendFactID(Digest("SPEND", tx[:], v.Encode(), effects[:], e.Data()))
}
func SignSpend(fact SpendFactID, member uint16, key ed25519.PrivateKey) SpendVote {
	v := SpendVote{Member: member}
	h := Digest("SPEND_VOTE", fact[:])
	copy(v.Signature[:], ed25519.Sign(key, h[:]))
	return v
}
func VerifyQC(q SpendQC, c OrgConfig) error {
	if c.Validate() != nil || len(q.Votes) < 3 || len(q.Votes) > 4 {
		return ErrAuth
	}
	seen := uint8(0)
	h := Digest("SPEND_VOTE", q.Fact[:])
	for _, v := range q.Votes {
		if v.Member >= 4 || seen&(1<<v.Member) != 0 {
			return ErrAuth
		}
		seen |= 1 << v.Member
		if !ed25519.Verify(c.Members[v.Member][:], h[:], v.Signature[:]) {
			return ErrAuth
		}
	}
	return nil
}
