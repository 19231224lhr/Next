package reservecontrol

import (
	"math"
	"testing"
	"time"
)

func TestDynamicCALTarget(t *testing.T) {
	for _, tt := range []struct {
		name                             string
		current, reserved, spent, demand uint64
		free                             [4]uint64
		want, available                  uint64
	}{
		{"quorum", 300, 0, 0, 100, [4]uint64{100, 80, 40, 10}, 390, 40},
		{"public occupancy", 300, 250, 20, 100, [4]uint64{200, 200, 200, 200}, 370, 200},
		{"no withdrawal", 300, 0, 0, 10, [4]uint64{100, 80, 40, 10}, 300, 40},
		{"rounding", 301, 0, 0, 41, [4]uint64{100, 80, 40, 10}, 302, 40},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, a, e := CALTarget(tt.current, tt.reserved, tt.spent, tt.demand, tt.free)
			if e != nil || got != tt.want || a != tt.available {
				t.Fatalf("target=%d available=%d err=%v", got, a, e)
			}
		})
	}
	if _, _, e := CALTarget(math.MaxUint64, math.MaxUint64, 1, 1, [4]uint64{}); e == nil {
		t.Fatal("overflow accepted")
	}
	if _, _, e := CALTarget(300, 0, 0, 1, [4]uint64{201, 0, 0, 0}); e == nil {
		t.Fatal("availability above ceiling")
	}
}

func TestDynamicHistoryKeepsBurstAndExpires(t *testing.T) {
	now := time.Unix(100, 0)
	h := DemandHistory{Window: time.Minute}
	for i := 0; i < 10; i++ {
		amount := uint64(10)
		if i == 0 {
			amount = 100
		}
		if e := h.Observe(now.Add(time.Duration(i)*time.Second), amount, time.Second); e != nil {
			t.Fatal(e)
		}
	}
	got, e := h.Forecast(now.Add(10*time.Second), 5*time.Second)
	if e != nil || got != 500 {
		t.Fatal("historical P95 lost", got, e)
	}
	got, e = h.Forecast(now.Add(80*time.Second), 5*time.Second)
	if e != nil || got != 0 {
		t.Fatal("expired demand retained", got, e)
	}
	if e = h.Observe(now, math.MaxUint64, time.Nanosecond); e != nil {
		t.Fatal(e)
	}
	if _, e = h.Forecast(now, 2*time.Second); e == nil {
		t.Fatal("forecast overflow accepted")
	}
}

func TestDynamicControllerWaitsForConfirmedQuorum(t *testing.T) {
	now := time.Unix(100, 0)
	c := Controller{Interval: time.Second, Lead: time.Second, Minimum: 100}
	pub := PublicState{Grant: 300}
	ms := [4]MemberState{}
	for i, a := range []uint64{100, 80, 40, 10} {
		ms[i] = MemberState{Valid: true, Grant: 300, Available: a}
	}
	inc, e := c.Step(now, pub, ms)
	if e != nil || inc == nil || inc.Previous != 300 || inc.Amount != 90 {
		t.Fatal(inc, e)
	}
	first := *inc
	// HTTP acceptance alone does not increase member quotas or create a second command.
	inc, e = c.Step(now.Add(time.Second), pub, ms)
	if e != nil || inc == nil || *inc != first {
		t.Fatal("pending command changed", inc, e)
	}
	missing := ms
	missing[0].Valid = false
	missing[1].Valid = false
	inc, e = c.Step(now.Add(1500*time.Millisecond), pub, missing)
	if e != nil || inc == nil || *inc != first {
		t.Fatal("sampling outage lost pending command", inc, e)
	}
	pub.Grant = 390
	for i := 0; i < 2; i++ {
		ms[i].Grant = 390
		ms[i].Available += 60
	}
	inc, e = c.Step(now.Add(2*time.Second), pub, ms)
	if e != nil || inc == nil || *inc != first {
		t.Fatal("only two members applied", inc, e)
	}
	ms[2].Grant = 390
	ms[2].Available += 60
	inc, e = c.Step(now.Add(3*time.Second), pub, ms)
	if e != nil || inc != nil {
		t.Fatal("confirmed refill duplicated", inc, e)
	}
	if c.Lead < 3*time.Second {
		t.Fatal("effective lead time not learned")
	}
}

func TestDynamicControllerCountsParallelGMApprovalsOnce(t *testing.T) {
	now := time.Unix(100, 0)
	c := Controller{Interval: time.Second, Lead: time.Second}
	pub := PublicState{Grant: 3000}
	ms := [4]MemberState{}
	for i := range ms {
		ms[i] = MemberState{Valid: true, Grant: 3000, Available: 2000}
	}
	if _, e := c.Step(now, pub, ms); e != nil {
		t.Fatal(e)
	}
	for i := range ms {
		ms[i].ApprovedCAL = 100
	}
	if _, e := c.Step(now.Add(time.Second), pub, ms); e != nil {
		t.Fatal(e)
	}
	if c.Demand != 200 {
		t.Fatalf("got %d; expected 100 CAL/s * (1s lead + 1s check), not 4x votes", c.Demand)
	}
}

func TestDynamicControllerRefillsExhaustedWorkerDespiteFreeTotal(t *testing.T) {
	c := Controller{Interval: time.Second, Lead: time.Second, Minimum: 100}
	var ms [4]MemberState
	for i := range ms {
		ms[i] = MemberState{Valid: true, Grant: 600, Available: 200, LargestCAL: 100, Slices: []uint64{0, 200}}
	}
	inc, e := c.Step(time.Unix(100, 0), PublicState{Grant: 600, Reserved: 200}, ms)
	if e != nil || inc == nil || inc.Previous != 600 || inc.Amount != 300 {
		t.Fatal("exhausted hash-selected worker ignored", inc, e)
	}
}

func TestDynamicControllerDoesNotRequireImpossibleFourthMember(t *testing.T) {
	c := Controller{Interval: time.Second, Lead: time.Second, Minimum: 2000000000000000000}
	const backing = uint64(12000000000000000000)
	var ms [4]MemberState
	for i := 0; i < 3; i++ {
		ms[i] = MemberState{Valid: true, Grant: backing, Available: 8000000000000000000, LargestCAL: c.Minimum, Slices: []uint64{c.Minimum, c.Minimum, c.Minimum, c.Minimum}}
	}
	ms[3] = MemberState{Valid: true, Grant: backing, LargestCAL: c.Minimum, Slices: []uint64{0, 0, 0, 0}}
	inc, e := c.Step(time.Unix(100, 0), PublicState{Grant: backing}, ms)
	if e != nil || inc != nil {
		t.Fatal("unneeded fourth member blocked a sufficient quorum", inc, e)
	}
}
