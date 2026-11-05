package member

import (
	"context"
	"testing"
	"time"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

// Evaluate a fresh approval against an unchanged genesis snapshot. This measures
// decoding, validation, quota changes, queue construction and the returned vote;
// it excludes I/O commit and networking, and does not benchmark cached retries.
type approvalEvaluationStore struct{ store.Store }

func (s approvalEvaluationStore) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	return s.Store.View(func(v state.ReadView) error { _, err := fn(v); return err })
}
func BenchmarkFreshApprovalDeadline(b *testing.B) {
	for _, deadline := range []bool{false, true} {
		name := "legacy"
		if deadline {
			name = "deadline"
		}
		b.Run(name, func(b *testing.B) {
			m, db, f, p := approvalReuseFixture(b, true)
			m.direct.RequireApprovalDeadline = deadline
			pay := approvalReusePayment(b, f, p)
			if deadline {
				p.RequireApprovalDeadline = true
				pay.Tx.Body.Rules = p.Rules()
				var err error
				pay.Tx, err = protocol.NewExpiringFastTx(pay.Tx.Body, pay.Tx.Claims, p.Key, time.Now().Unix())
				if err != nil {
					b.Fatal(err)
				}
				pay.Tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(pay.Tx.ID(), f.Owner)}
			}
			raw, err := (protocol.DirectRequest{Tx: pay.Tx}).MarshalBinary()
			if err != nil {
				b.Fatal(err)
			}
			m.db = approvalEvaluationStore{db}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := m.ApproveDirectBytes(context.Background(), raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
