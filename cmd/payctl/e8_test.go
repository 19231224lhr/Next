package main

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func TestE8SlotsDoNotCatchUpInBursts(t *testing.T) {
	start := time.Unix(100, 0)
	due, skipped := e8NextSlot(start, start.Add(105*time.Millisecond), 10*time.Millisecond)
	if !due.Equal(start.Add(110*time.Millisecond)) || skipped != 10 {
		t.Fatalf("%v %d", due, skipped)
	}
}

func TestE8ObservationDurationDoesNotUseRecordedWallTime(t *testing.T) {
	start := time.Now()
	s := e8Sample{sentAt: start, SentNS: start.Add(40 * time.Millisecond).UnixNano()}
	s.observePublic(start.Add(5 * time.Millisecond))
	s.observeMember(2, start.Add(7*time.Millisecond))
	if s.Monotonic.PublicNS != int64(5*time.Millisecond) || s.Monotonic.MemberNS[2] != int64(7*time.Millisecond) {
		t.Fatalf("duration followed wall-time fields: %+v", s.Monotonic)
	}
}

func TestE8ChainFeeInputsStayBalanced(t *testing.T) {
	for _, seed := range []int64{0, 124, 225, 326} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			lanes := make([]e8Lane, 64)
			for i := range lanes {
				lanes[i].Owner = -1
			}
			fees := make([]int, 2048)
			for i := 0; i < 152048; i++ {
				which := i % len(lanes)
				if seed != 0 && i >= len(lanes) {
					which = rng.Intn(len(lanes))
				}
				lane := &lanes[which]
				to := (124 + i) % len(fees)
				if lane.Hop == 0 {
					lane.Owner = e8RootOwner(true, *lane, to, len(fees))
				}
				if to == lane.Owner {
					to = (to + 1) % len(fees)
				}
				fees[lane.Owner]++
				lane.Owner = to
				lane.Hop = (lane.Hop + 1) % 10
			}
			for owner, n := range fees {
				if n > 82 {
					t.Fatalf("wallet %d needs %d fee inputs, pool has 82", owner, n)
				}
			}
		})
	}
	if got := e8RootOwner(false, e8Lane{Owner: 700}, 10, 2048); got != 9 {
		t.Fatalf("independent workload changed: %d", got)
	}
}
