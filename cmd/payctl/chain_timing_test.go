package main

import (
	"context"
	"errors"
	"testing"
	"utxo/finality"
)

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
