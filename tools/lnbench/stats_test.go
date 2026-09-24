package main

import "testing"

func TestPercentile(t *testing.T) {
	if percentile(nil, .95) != 0 {
		t.Fatal("empty")
	}
	v := []float64{4, 1, 3, 2}
	if percentile(v, .5) != 2 || percentile(v, .95) != 4 {
		t.Fatal("nearest rank")
	}
	if v[0] != 4 {
		t.Fatal("mutated samples")
	}
}

func TestSummaryIncludesFailuresAndUnsent(t *testing.T) {
	s := summarize([]sample{
		{Sent: 1, Settled: 11, Success: 21, Done: 21, Status: "SUCCEEDED"},
		{Sent: 2, Done: 32, Status: "FAILED"},
		{Status: "NOT_SENT"},
	}, 100)
	if s.Planned != 3 || s.Sent != 2 || s.Succeeded != 1 || s.Settled != 1 || s.Failed != 1 || s.Unsent != 1 {
		t.Fatalf("%+v", s)
	}
}
