package protocol

import "testing"

func TestFinalC3PlanAndWire(t *testing.T) {
	c := testC3()
	c.Version = 2
	c.Quote = 0
	c.Sponsor = Hash{}
	c.Payer = PublicKey{1}
	c.Schedule = FeeSchedule{AttachReward: 1, ExecuteReward: 2, ResolveReward: 3, Compensate: 4, Repay: 5}
	c.Budget = 15
	if e := c.Validate(); e != nil {
		t.Fatal("final free CAL quote rejected", e)
	}
	raw, e := c.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	got, e := DecodeFeeCommitment(raw)
	if e != nil || got != c {
		t.Fatal("final wire", e)
	}
	c.Schedule.Open = 1
	if c.Validate() == nil {
		t.Fatal("final normal disorder charged")
	}
}
