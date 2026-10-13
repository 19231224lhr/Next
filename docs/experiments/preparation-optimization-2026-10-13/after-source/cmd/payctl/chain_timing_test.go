package main

import (
	"context"
	"errors"
	"testing"
	"utxo/finality"
)

func TestChainCommitNotificationsDefaultAndOverride(t *testing.T) {
	for _, tc := range []struct {
		value   string
		enabled bool
	}{{"", true}, {"1", true}, {"0", false}} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("UTXO_CHAIN_COMMIT_NOTIFICATIONS", tc.value)
			if got := chainCommitNotificationsEnabled(); got != tc.enabled {
				t.Fatalf("value %q: enabled=%v, want %v", tc.value, got, tc.enabled)
			}
		})
	}
}

func TestChainCommitWakesAllSubscribers(t *testing.T) {
	s := &chainTimingSource{rows: make(map[int64]chainFollowTiming)}
	first, second := s.commitNotification(), s.commitNotification()
	s.committed(1)
	for i, notification := range []<-chan struct{}{first, second} {
		select {
		case <-notification:
		default:
			t.Fatalf("subscriber %d missed commit", i)
		}
	}
}

func TestChainCommitNotificationTracksSuccessiveCommits(t *testing.T) {
	s := &chainTimingSource{rows: make(map[int64]chainFollowTiming)}
	first := s.commitNotification()
	select {
	case <-first:
		t.Fatal("notification fired before commit")
	default:
	}
	s.verified(1)
	select {
	case <-first:
		t.Fatal("verification woke observer before wallet commit")
	default:
	}
	s.committed(1)
	select {
	case <-first:
	default:
		t.Fatal("commit did not wake observer")
	}
	second := s.commitNotification()
	select {
	case <-second:
		t.Fatal("new observer received stale commit notification")
	default:
	}
	s.committed(2)
	select {
	case <-second:
	default:
		t.Fatal("second commit did not wake observer")
	}
}

type timingSource struct{ calls int }

func (s *timingSource) Block(context.Context, int64) (finality.BlockData, error) {
	s.calls++
	if s.calls == 1 {
		return finality.BlockData{}, errors.New("not available")
	}
	return finality.BlockData{}, nil
}

func TestChainTimingSeparatesFailedFetchFromLocalCommit(t *testing.T) {
	s := &chainTimingSource{Source: &timingSource{}}
	if _, err := s.Block(context.Background(), 7); err == nil {
		t.Fatal("expected unsuccessful fetch")
	}
	rows := s.snapshot()
	if len(rows) != 1 || rows[0].FetchDoneNS != 0 || rows[0].CommittedNS != 0 {
		t.Fatal("failed fetch reported completion", rows)
	}
	if _, err := s.Block(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	s.verified(7)
	s.committed(7)
	r := s.snapshot()[0]
	if r.Attempts != 2 || !(r.FirstAttemptNS <= r.LastFailedNS && r.LastFailedNS <= r.FetchStartNS && r.FetchStartNS <= r.FetchDoneNS && r.FetchDoneNS <= r.VerifiedNS && r.VerifiedNS <= r.CommittedNS) {
		t.Fatalf("invalid event order: %+v", r)
	}
	rows[0].Height = 99
	if s.snapshot()[0].Height != 7 {
		t.Fatal("snapshot aliases live state")
	}
}
