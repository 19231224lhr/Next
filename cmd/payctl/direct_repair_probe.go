package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
	"utxo/internal/redaction"
	"utxo/internal/rules"
	"utxo/protocol"
)

type repairNodeObservation struct {
	URL                                 string
	CommittedUnixNS, MaterializedUnixNS int64
	Snapshot                            redaction.Observation
}

type repairTrial struct {
	DeadlineUnix, DeadlineObservedUnixNS int64
	ProbeRequests, ProbeErrors           int
	ProbeTimeNS, MaxProbeNS              int64
	LastProbeError                       string `json:",omitempty"`
	Nodes                                []repairNodeObservation
}

// waitRepair never publishes on timeout. Observed timestamps are local polling
// times, not the exact remote Commit/physical-write timestamps.
func waitRepair(ctx context.Context, client *http.Client, urls []string, output protocol.OutputID, trial *repairTrial) error {
	get := func(url string, value any) error {
		trial.ProbeRequests++
		started := time.Now()
		defer func() {
			elapsed := time.Since(started).Nanoseconds()
			trial.ProbeTimeNS += elapsed
			trial.MaxProbeNS = max(trial.MaxProbeNS, elapsed)
		}()
		rctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(rctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("repair probe HTTP %d", resp.StatusCode)
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(value)
	}
	failed := func(err error) { trial.ProbeErrors++; trial.LastProbeError = err.Error() }
	id := protocol.Hash(output).String()
	// Use the actual consensus-anchored deadline; do not hammer every historical
	// store while the protocol is deliberately waiting for the parent.
	for trial.DeadlineUnix == 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		var ob rules.DirectObligation
		if err := get(urls[0]+"/v3/obligations/"+id, &ob); err != nil {
			failed(err)
		} else if ob.Deadline > 0 {
			trial.DeadlineUnix, trial.DeadlineObservedUnixNS = ob.Deadline, time.Now().UnixNano()
			break
		}
		if err := budgetPause(ctx, 250*time.Millisecond); err != nil {
			return err
		}
	}
	if err := budgetPause(ctx, time.Until(time.Unix(trial.DeadlineUnix, 0))); err != nil {
		return err
	}
	trial.Nodes = make([]repairNodeObservation, len(urls))
	for i, url := range urls {
		trial.Nodes[i].URL = url
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		complete := 0
		for i := range trial.Nodes {
			n := &trial.Nodes[i]
			if n.MaterializedUnixNS != 0 {
				complete++
				continue
			}
			var s redaction.Observation
			if err := get(n.URL+"/v3/repairs/"+id+"/status", &s); err != nil {
				failed(err)
				continue
			}
			n.Snapshot = s
			now := time.Now().UnixNano()
			if s.Committed && n.CommittedUnixNS == 0 {
				n.CommittedUnixNS = now
			}
			if s.Committed && s.Materialized && s.IdentityStable && s.BytesChanged {
				n.MaterializedUnixNS = now
				complete++
			}
		}
		if complete == len(urls) {
			return nil
		}
		if err := budgetPause(ctx, 250*time.Millisecond); err != nil {
			return err
		}
	}
}

// Each hundred offered units has a fixed shuffled ordering. Taking a prefix
// makes the 1% group a subset of 5%, without selecting from successful units.
func repairSelection(total, percent int, seed int64) []bool {
	selected := make([]bool, total)
	rng := rand.New(rand.NewSource(seed))
	for base := 0; base < total; base += 100 {
		size := min(100, total-base)
		order := rng.Perm(size)
		for _, i := range order[:size*percent/100] {
			selected[base+i] = true
		}
	}
	return selected
}
