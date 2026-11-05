package rules

import (
	"testing"
	"utxo/protocol"
)

func TestB01CumulativeCreditDoesNotReturnSpentFunds(t *testing.T) {
	current := CreditState{Original: 100, Remaining: 100}
	first := CreditState{Original: 100, Paid: 94, Discharged: 6, Remaining: 0, Revision: 1}
	next, delta, e := ApplyCredit(current, first)
	if e != nil || delta != 6 {
		t.Fatalf("close: %d %v", delta, e)
	}
	again, delta, e := ApplyCredit(next, first)
	if e != nil || delta != 0 || again != next {
		t.Fatal("duplicate credit")
	}
	later := first
	later.Returned = 40
	later.Revision = 2
	next, delta, e = ApplyCredit(next, later)
	if e != nil || delta != 40 {
		t.Fatal("real return")
	}
	stale, delta, e := ApplyCredit(next, first)
	if e != nil || delta != 0 || stale != next {
		t.Fatal("stale proof")
	}
	if residual, e := next.Residual(); e != nil || residual != 54 {
		t.Fatal("net expenditure disappeared")
	}
	bad := later
	bad.Returned = 95
	bad.Revision = 3
	if _, _, e := ApplyCredit(next, bad); e == nil {
		t.Fatal("over-return")
	}
	bad = later
	bad.Original = 101
	if _, _, e := ApplyCredit(next, bad); e == nil {
		t.Fatal("wrong cap")
	}
}
func TestCreditRejectsInconsistentNewRevision(t *testing.T) {
	a := CreditState{Original: 100, Paid: 70, Discharged: 10, Remaining: 20, Revision: 1}
	b := CreditState{Original: 100, Paid: 60, Discharged: 40, Revision: 2}
	if _, _, e := ApplyCredit(a, b); e == nil {
		t.Fatal("paid decreased")
	}
	b = a
	b.Returned = 1
	if _, _, e := ApplyCredit(a, b); e == nil {
		t.Fatal("same revision changed")
	}
	if _, e := (CreditState{Original: 1, Paid: ^uint64(0), Discharged: 2}).Residual(); e == nil {
		t.Fatal("overflow")
	}
}
func TestF02FeeCloseAndActionIdempotence(t *testing.T) {
	p := FeePlan{Register: 20, Settle: 40, Close: 34, Burn: 10}
	f, e := NewEscrow(100, p)
	if e != nil {
		t.Fatal(e)
	}
	f, e = f.Complete(FeeRegister)
	if e != nil {
		t.Fatal(e)
	}
	same, e := f.Complete(FeeRegister)
	if e != nil || same != f {
		t.Fatal("duplicate charged")
	}
	if _, e = f.Complete(FeeClose); e == nil {
		t.Fatal("premature close")
	}
	f, e = f.Complete(FeeSettle)
	if e != nil {
		t.Fatal(e)
	}
	f, e = f.Complete(FeeClose)
	if e != nil {
		t.Fatal(e)
	}
	if f.Held != 0 || f.Refunded != 6 || f.Rewards != 84 || f.Burned != 10 || !f.Closed {
		t.Fatalf("%+v", f)
	}
	same, e = f.Complete(FeeClose)
	if e != nil || same != f {
		t.Fatal("close replay")
	}
}
func TestEscrowRejectsInsufficientAndOverflow(t *testing.T) {
	if _, e := NewEscrow(10, FeePlan{Register: 20}); e == nil {
		t.Fatal("unfunded plan")
	}
	if _, e := NewEscrow(^uint64(0), FeePlan{Register: ^uint64(0), Settle: 1}); e != protocol.ErrAmount {
		t.Fatalf("overflow %v", e)
	}
}
