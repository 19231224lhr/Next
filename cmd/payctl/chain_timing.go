package main

import (
	"context"
	"sort"
	"sync"
	"time"
	"utxo/finality"
	"utxo/internal/blockfollow"
)

// Client observations only. FetchDone is successful receipt of the complete
// evidence, not its earliest availability at a committee. No protocol state is
// changed, and the normal follower still verifies and commits every block.
type chainFollowTiming struct {
	Height, FirstAttemptNS, LastFailedNS, FetchStartNS, FetchDoneNS int64
	VerifiedNS, CommittedNS                                         int64
	Attempts                                                        int
}

type chainTimingSource struct {
	blockfollow.Source
	mu   sync.Mutex
	rows map[int64]chainFollowTiming
}

func (s *chainTimingSource) Block(ctx context.Context, height int64) (finality.BlockData, error) {
	start := time.Now().UnixNano()
	b, err := s.Source.Block(ctx, height)
	end := time.Now().UnixNano()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows == nil {
		s.rows = make(map[int64]chainFollowTiming)
	}
	r := s.rows[height]
	r.Height = height
	r.Attempts++
	if r.FirstAttemptNS == 0 {
		r.FirstAttemptNS = start
	}
	if err == nil {
		r.FetchStartNS, r.FetchDoneNS = start, end
	} else {
		r.LastFailedNS = end
	}
	s.rows[height] = r
	return b, err
}

func (s *chainTimingSource) verified(height int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[height]
	r.VerifiedNS = time.Now().UnixNano()
	s.rows[height] = r
}

func (s *chainTimingSource) committed(height int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[height]
	r.CommittedNS = time.Now().UnixNano()
	s.rows[height] = r
}

func (s *chainTimingSource) snapshot() []chainFollowTiming {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([]chainFollowTiming, 0, len(s.rows))
	for _, r := range s.rows {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Height < rows[j].Height })
	return rows
}
