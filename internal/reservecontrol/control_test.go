package reservecontrol

import "testing"

func TestWatermarksAndCap(t *testing.T) {
	p := Policy{DemandPerSecond: 4000, LeadMillis: 500, Burst: 200, Unit: 100, Maximum: 14400}
	low, target, delta, err := p.Decide(7200, 800)
	if err != nil || low != 2200 || target != 2750 || delta != 3000 {
		t.Fatal(low, target, delta, err)
	}
	_, _, delta, err = p.Decide(14400, 0)
	if err != nil || delta != 0 {
		t.Fatal(delta, err)
	}
	_, _, delta, err = p.Decide(7200, 2200)
	if err != nil || delta != 0 {
		t.Fatal("at low watermark", delta, err)
	}
	_, _, delta, err = p.Decide(14399, 0)
	if err != nil || delta != 1 {
		t.Fatal("remaining cap", delta, err)
	}
	p.DemandPerSecond = ^uint64(0)
	if _, _, _, err = p.Decide(1, 0); err == nil {
		t.Fatal("overflow")
	}
}
