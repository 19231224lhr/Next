package rules

import (
	"encoding/json"
	"testing"

	"utxo/protocol"
)

func TestDirectParentVerificationPreservesAuthentication(t *testing.T) {
	mutations := map[string]func(*protocol.OutputCertificate){
		"signature": func(c *protocol.OutputCertificate) { c.QC.Votes[0].Signature[0] ^= 1 },
		"member":    func(c *protocol.OutputCertificate) { c.QC.Votes[0].Member = c.QC.Votes[1].Member },
		"short_qc":  func(c *protocol.OutputCertificate) { c.QC.Votes = c.QC.Votes[:2] },
		"long_qc":   func(c *protocol.OutputCertificate) { c.QC.Votes = append(c.QC.Votes, c.QC.Votes[0], c.QC.Votes[1]) },
		"fact":      func(c *protocol.OutputCertificate) { c.QC.Fact[0] ^= 1 },
		"epoch":     func(c *protocol.OutputCertificate) { c.Summary.Epoch++ },
		"output":    func(c *protocol.OutputCertificate) { c.Summary.Outputs[0].Digest[0] ^= 1 },
		"grants":    func(c *protocol.OutputCertificate) { c.Summary.Grants = nil },
		"index":     func(c *protocol.OutputCertificate) {},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			f := c3Fixture(t)
			parent := c3Payment(t, f, nil, 1)
			p := c3Payment(t, f, &parent.Certificate, 2).Submission()
			mutate(&p.InputCertificates[0].Certificate)
			if name == "index" {
				p.InputCertificates[0].Index = 1
			}
			if _, err := VerifyDirectSubmission(p, f.policy); err == nil {
				t.Fatal("malformed parent accepted")
			}
		})
	}
}

func TestC3VerifiedParentOwnsEverySlice(t *testing.T) {
	f := c3Fixture(t)
	parent := c3Payment(t, f, nil, 1)
	p := c3Payment(t, f, &parent.Certificate, 2).Submission()
	v, err := VerifyDirectSubmission(p, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	id := parent.Certificate.Summary.OutputID(0)
	before, _ := json.Marshal(v.parents[id])
	c := &p.InputCertificates[0].Certificate
	c.Summary.Outputs[0].Amount++
	c.Summary.Admission[0].Cap++
	c.Summary.Grants[0][0] ^= 1
	c.QC.Votes[0].Signature[0] ^= 1
	after, _ := json.Marshal(v.parents[id])
	if string(before) != string(after) {
		t.Fatal("caller mutation changed verified parent")
	}
}

func BenchmarkVerifyDirectSubmissionC3Parent(b *testing.B) {
	f := c3Fixture(b)
	parent := c3Payment(b, f, nil, 1)
	p := c3Payment(b, f, &parent.Certificate, 2).Submission()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := VerifyDirectSubmission(p, f.policy); err != nil {
			b.Fatal(err)
		}
	}
}
