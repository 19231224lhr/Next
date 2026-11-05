package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
	"utxo/internal/member"
	"utxo/protocol"
)

type progressResult struct {
	status member.DirectStatus
	err    error
}
type progressCheck struct {
	fact     protocol.SpendFactID
	enqueued time.Time
	result   chan progressResult
}

// One bounded queue per member coalesces observation RPCs, not payment work.
// Every caller still receives the status of its own exact fact.
type progressBatcher struct {
	ctx                       context.Context
	queue                     chan progressCheck
	done                      chan struct{}
	requests, failures, facts atomic.Uint64
	httpNS, queueNS           atomic.Uint64
}

func newProgressBatcher(ctx context.Context, capacity int, load func(context.Context, []protocol.SpendFactID) ([]member.DirectStatus, error)) *progressBatcher {
	b := &progressBatcher{ctx: ctx, queue: make(chan progressCheck, capacity), done: make(chan struct{})}
	go func() {
		defer close(b.done)
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			batch := make([]progressCheck, 0, member.MaxProgressBatch)
			for len(batch) < member.MaxProgressBatch {
				select {
				case q := <-b.queue:
					batch = append(batch, q)
				default:
					goto collected
				}
			}
		collected:
			if len(batch) == 0 {
				continue
			}
			facts := make([]protocol.SpendFactID, len(batch))
			for i, q := range batch {
				facts[i] = q.fact
			}
			started := time.Now()
			for _, q := range batch {
				b.queueNS.Add(uint64(started.Sub(q.enqueued)))
			}
			b.requests.Add(1)
			b.facts.Add(uint64(len(facts)))
			httpStarted := time.Now()
			statuses, err := load(ctx, facts)
			b.httpNS.Add(uint64(time.Since(httpStarted)))
			if err == nil && len(statuses) != len(batch) {
				err = protocol.ErrEncoding
			}
			if err != nil {
				b.failures.Add(1)
			}
			for i, q := range batch {
				result := progressResult{err: err}
				if err == nil {
					result.status = statuses[i]
				}
				q.result <- result
			}
		}
	}()
	return b
}
func (b *progressBatcher) Check(ctx context.Context, f protocol.SpendFactID) (member.DirectStatus, error) {
	q := progressCheck{fact: f, enqueued: time.Now(), result: make(chan progressResult, 1)}
	select {
	case <-ctx.Done():
		return member.DirectStatus{}, ctx.Err()
	case <-b.ctx.Done():
		return member.DirectStatus{}, b.ctx.Err()
	case b.queue <- q:
	}
	select {
	case <-ctx.Done():
		return member.DirectStatus{}, ctx.Err()
	case <-b.ctx.Done():
		return member.DirectStatus{}, b.ctx.Err()
	case r := <-q.result:
		return r.status, r.err
	}
}
func loadProgressBatch(ctx context.Context, c *http.Client, url string, facts []protocol.SpendFactID) ([]member.DirectStatus, error) {
	raw, err := json.Marshal(facts)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", url+"/v4/progress", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("progress HTTP %d", response.StatusCode)
	}
	var statuses []member.DirectStatus
	err = json.Unmarshal(raw, &statuses)
	return statuses, err
}
