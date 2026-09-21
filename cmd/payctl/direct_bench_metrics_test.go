package main

import (
	"testing"
	"time"
)

func TestDirectQueueKeepsUnknownAndSeparatesReservations(t *testing.T) {
	start := time.Unix(1, 0)
	ms := func(n int64) int64 { return start.Add(time.Duration(n) * time.Millisecond).UnixNano() }
	samples := []directBenchSample{
		{SentUnixNS: ms(0), FastUnixNS: ms(250), BlockObservedUnixNS: ms(500), MemberObservedUnixNS: ms(750)},
		{SentUnixNS: ms(100), TaskDoneUnixNS: ms(300), Error: "timeout", Outcome: "UNKNOWN"},
		{ScheduledUnixNS: ms(0), Dispatch: directDispatchWait{TotalAcquiredUnixNS: ms(200), TotalReleasedUnixNS: ms(350)}},
	}
	rows := directQueueTimeline(samples, start, start.Add(time.Second))
	if len(rows) != 5 {
		t.Fatal(rows)
	}
	r := rows[1]
	if r.ReservedNotSent != 1 || r.AwaitingFast != 1 || r.AwaitingBackground != 1 || r.SentUnfinished != 2 {
		t.Fatal(r)
	}
	last := rows[len(rows)-1]
	if last.SentUnfinished != 1 || last.OldestUnfinishedMS != 900 || last.ReservedNotSent != 0 {
		t.Fatal("unknown was incorrectly completed", last)
	}
	summary := map[string]any{}
	addDirectBenchMetrics(summary, samples, rows, time.Second)
	if summary["unknown"] != 1 || summary["member_completed"] != 1 || summary["fast_completed"] != 1 || summary["not_sent"] != 1 {
		t.Fatal(summary)
	}
}
