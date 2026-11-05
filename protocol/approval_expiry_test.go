package protocol

import (
	"bytes"
	"testing"
)

func TestApprovalDeadlineSignedAndRoundTripped(t *testing.T) {
	for _, c3 := range []bool{false, true} {
		tx, key := initialTxFixture(t, c3)
		_, owner, cfg, keys := c3TxFixture()
		fresh, err := NewExpiringFastTx(tx.Body, tx.Claims, key, 1000)
		if err != nil || fresh.Body.ApprovalDeadline != 1000+ApprovalLifetimeSeconds {
			t.Fatal("default D_T", err)
		}
		tx = fresh
		tx.Auth = []OwnerAuth{SignOwner(tx.ID(), owner)}
		raw, err := tx.MarshalInitial(key)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeFastTx(raw)
		if err != nil || decoded.Body.ApprovalDeadline != tx.Body.ApprovalDeadline || decoded.VerifyInitial(key) != nil {
			t.Fatal("deadline lost", err)
		}
		altered := decoded
		altered.Body.ApprovalDeadline++
		if altered.VerifyAuth() == nil {
			t.Fatal("wallet signature did not bind D_T")
		}
		same, err := NewExpiringFastTx(tx.Body, tx.Claims, key, 2000)
		if err != nil || same.Body.ApprovalDeadline != tx.Body.ApprovalDeadline {
			t.Fatal("retry refreshed D_T", err)
		}
		vector := AdmissionVector{{Key: tx.Body.Admission[0].Key, Cap: 1}}
		summary := SummaryFor(tx, vector)
		cert := OutputCertificate{Summary: summary, QC: SpendQC{Fact: summary.Fact()}}
		for i := 0; i < 3; i++ {
			cert.QC.Votes = append(cert.QC.Votes, SignSpend(summary.Fact(), uint16(i), keys[i]))
		}
		wire, err := cert.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeOutputCertificate(wire)
		if err != nil || got.Verify(cfg) != nil || got.Summary.ApprovalDeadline != tx.Body.ApprovalDeadline {
			t.Fatal("GM signed deadline roundtrip", err)
		}
		again, _ := got.MarshalBinary()
		if !bytes.Equal(wire, again) {
			t.Fatal("noncanonical certificate")
		}
		got.Summary.ApprovalDeadline++
		got.QC.Fact = got.Summary.Fact()
		if got.Verify(cfg) == nil {
			t.Fatal("GM signatures did not bind D_T")
		}
	}
}

func BenchmarkApprovalDeadlineMarshal(b *testing.B) {
	for _, expiry := range []bool{false, true} {
		name := "legacy"
		if expiry {
			name = "deadline"
		}
		b.Run(name, func(b *testing.B) {
			tx, key := initialTxFixture(b, true)
			if expiry {
				_, owner, _, _ := c3TxFixture()
				var err error
				tx, err = NewExpiringFastTx(tx.Body, tx.Claims, key, 1000)
				if err != nil {
					b.Fatal(err)
				}
				tx.Auth = []OwnerAuth{SignOwner(tx.ID(), owner)}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := tx.MarshalInitial(key); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
