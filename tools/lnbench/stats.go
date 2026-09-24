package main

import (
	"math"
	"sort"
)

type sample struct {
	Index   int    `json:"index"`
	Sender  int    `json:"sender"`
	Hash    string `json:"hash"`
	Planned int64  `json:"planned_ns"`
	Sent    int64  `json:"sent_ns"`
	Settled int64  `json:"settled_ns"`
	Success int64  `json:"success_ns"`
	Done    int64  `json:"done_ns"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	FeeMSat int64  `json:"fee_msat"`
}
type quantiles struct{ P50, P95, P99, Max float64 }
type summary struct {
	Planned, Sent, Succeeded, Settled, Failed, Unsent int
	ElapsedSeconds, SuccessTPS                        float64
	ReceiverMS, SenderMS, DispatchMS                  quantiles
}

func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	a := append([]float64(nil), v...)
	sort.Float64s(a)
	i := int(math.Ceil(p*float64(len(a)))) - 1
	if i < 0 {
		i = 0
	}
	return a[i]
}
func qs(v []float64) quantiles {
	return quantiles{percentile(v, .5), percentile(v, .95), percentile(v, .99), percentile(v, 1)}
}
func summarize(rows []sample, elapsed int64) summary {
	s := summary{Planned: len(rows), ElapsedSeconds: float64(elapsed) / 1e9}
	var receiver, sender, dispatch []float64
	for _, r := range rows {
		if r.Sent == 0 {
			s.Unsent++
			continue
		}
		s.Sent++
		dispatch = append(dispatch, float64(r.Sent-r.Planned)/1e6)
		if r.Settled > 0 {
			s.Settled++
			receiver = append(receiver, float64(r.Settled-r.Sent)/1e6)
		}
		if r.Status == "SUCCEEDED" {
			s.Succeeded++
			sender = append(sender, float64(r.Success-r.Sent)/1e6)
		} else {
			s.Failed++
		}
	}
	if elapsed > 0 {
		s.SuccessTPS = float64(s.Succeeded) / s.ElapsedSeconds
	}
	s.ReceiverMS = qs(receiver)
	s.SenderMS = qs(sender)
	s.DispatchMS = qs(dispatch)
	return s
}
