package main

import (
	"sort"
	"time"
	"utxo/internal/requesttrace"
)

type directBenchSample struct {
	WalletQueries                                                                                                int
	PacingWaitMS                                                                                                 float64
	DispatchLagMS, DispatchQueueMS                                                                               float64
	ScheduledUnixNS                                                                                              int64
	Index                                                                                                        int
	SentUnixNS, CertificateReceivedUnixNS, FastUnixNS, BlockObservedUnixNS, MemberObservedUnixNS, TaskDoneUnixNS int64
	Foreground                                                                                                   []requesttrace.Event `json:",omitempty"`
	FastMS, BlockObservedMS, MemberAppliedMS                                                                     float64
	Fact, Outcome                                                                                                string
	Error                                                                                                        string `json:",omitempty"`
	Dispatch                                                                                                     directDispatchWait
	ProgressRequests, ProgressErrors                                                                             int
	ProgressHTTPMS, ProgressMaxMS                                                                                float64
}

func (s *directBenchSample) recordProgressQuery(start time.Time, ok bool) {
	d := float64(time.Since(start)) / float64(time.Millisecond)
	s.ProgressHTTPMS += d
	s.ProgressMaxMS = max(s.ProgressMaxMS, d)
	if !ok {
		s.ProgressErrors++
	}
}

type directQueueSample struct {
	UnixNS                                                                              int64
	Seconds                                                                             float64
	ScheduledNotSent, ReservedNotSent, AwaitingFast, AwaitingBackground, SentUnfinished int
	OldestUnfinishedMS                                                                  float64
}

// Reconstruct after joining tasks: no measurement goroutine races with samples.
// UNKNOWN outcomes remain unfinished even after their HTTP/observer task exits.
func directQueueTimeline(samples []directBenchSample, start, end time.Time) []directQueueSample {
	var rows []directQueueSample
	for now := start; ; now = now.Add(250 * time.Millisecond) {
		if now.After(end) {
			now = end
		}
		t := now.UnixNano()
		r := directQueueSample{UnixNS: t, Seconds: float64(t-start.UnixNano()) / 1e9}
		oldest := t
		for _, s := range samples {
			sent := s.SentUnixNS != 0 && s.SentUnixNS <= t
			if !sent {
				if s.ScheduledUnixNS != 0 && s.ScheduledUnixNS <= t {
					r.ScheduledNotSent++
				}
				w := s.Dispatch
				if w.TotalAcquiredUnixNS != 0 && w.TotalAcquiredUnixNS <= t && (w.TotalReleasedUnixNS == 0 || w.TotalReleasedUnixNS > t) {
					r.ReservedNotSent++
				}
				continue
			}
			if s.MemberObservedUnixNS != 0 && s.MemberObservedUnixNS <= t {
				continue
			}
			r.SentUnfinished++
			oldest = min(oldest, s.SentUnixNS)
			if s.FastUnixNS != 0 && s.FastUnixNS <= t {
				r.AwaitingBackground++
			} else {
				r.AwaitingFast++
			}
		}
		if r.SentUnfinished > 0 {
			r.OldestUnfinishedMS = float64(t-oldest) / 1e6
		}
		rows = append(rows, r)
		if now.Equal(end) {
			return rows
		}
	}
}

func addDirectBenchMetrics(summary map[string]any, samples []directBenchSample, queue []directQueueSample, elapsed time.Duration) {
	totalWait, sendWait, deliveryWait := []float64{}, []float64{}, []float64{}
	totalLimited, sendLimited, requests, queryErrors, fast, blocks, complete, unknown, unsent := 0, 0, 0, 0, 0, 0, 0, 0, 0
	var queryMS, maxQueryMS float64
	walletQueries := 0
	pacingWait := []float64{}
	for _, s := range samples {
		if s.Dispatch.ReceivedUnixNS != 0 {
			totalWait = append(totalWait, s.Dispatch.TotalWaitMS)
			sendWait = append(sendWait, s.Dispatch.SendWaitMS)
			deliveryWait = append(deliveryWait, s.DispatchQueueMS)
		}
		if s.Dispatch.TotalLimited {
			totalLimited++
		}
		if s.Dispatch.SendLimited {
			sendLimited++
		}
		walletQueries += s.WalletQueries
		if s.SentUnixNS != 0 {
			pacingWait = append(pacingWait, s.PacingWaitMS)
		}
		requests += s.ProgressRequests
		queryErrors += s.ProgressErrors
		queryMS += s.ProgressHTTPMS
		maxQueryMS = max(maxQueryMS, s.ProgressMaxMS)
		if s.FastUnixNS != 0 {
			fast++
		}
		if s.BlockObservedUnixNS != 0 {
			blocks++
		}
		if s.MemberObservedUnixNS != 0 {
			complete++
		} else if s.SentUnixNS != 0 {
			unknown++
		} else {
			unsent++
		}
	}
	for name, values := range map[string][]float64{"send_pacing_wait": pacingWait, "total_permit_wait": totalWait, "send_permit_wait": sendWait, "dispatch_delivery_wait": deliveryWait} {
		sort.Float64s(values)
		if len(values) > 0 {
			summary[name+"_p95_ms"] = values[int(float64(len(values)-1)*.95)]
		}
	}
	summary["total_limit_hits"], summary["send_limit_hits"] = totalLimited, sendLimited
	summary["fast_completed"], summary["wallet_block_observed"], summary["member_completed"] = fast, blocks, complete
	summary["unknown"], summary["not_sent"] = unknown, unsent
	summary["wallet_queries"] = walletQueries
	summary["progress_requests"], summary["progress_errors"] = requests, queryErrors
	summary["progress_requests_per_second"] = float64(requests) / elapsed.Seconds()
	summary["progress_http_max_ms"] = maxQueryMS
	if requests > 0 {
		summary["progress_http_mean_ms"] = queryMS / float64(requests)
	}
	peak := 0
	var oldest float64
	for _, row := range queue {
		peak = max(peak, row.SentUnfinished)
		oldest = max(oldest, row.OldestUnfinishedMS)
	}
	summary["sampled_peak_unfinished"], summary["sampled_oldest_unfinished_max_ms"] = peak, oldest
	if len(queue) > 0 {
		summary["final_unfinished"] = queue[len(queue)-1].SentUnfinished
	}
}
