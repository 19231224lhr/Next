//go:build comet_v3

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"utxo/internal/redaction"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

// This loop never requests adaptation shares. An unavailable repair service
// therefore cannot delay an otherwise executable public compensation decision.
// The durable due index remains authoritative; acceptance is not completion.
func (w *repairWorker) runDecisions(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var cursor []byte
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cursor = w.decisionStep(ctx, cursor)
		}
	}
}

func (w *repairWorker) decisionStep(ctx context.Context, cursor []byte) []byte {
	entries, err := store.Scan(w.db, state.Key(112), cursor, 64)
	if err != nil {
		return cursor
	}
	if len(entries) == 0 {
		return nil
	}
	height, publicNow := w.head()
	for i, entry := range entries {
		if ctx.Err() != nil {
			return cursor
		}
		cursor = entry.Key
		var ob rules.DirectObligation
		if json.Unmarshal(entry.Value, &ob) != nil {
			continue
		}
		if time.Now().Unix() < ob.Deadline+int64(w.index) {
			return nil
		}
		if publicNow < ob.Deadline {
			_, _ = w.submit(ctx, protocol.ClockTick(w.network, height+1))
			return nil
		}
		var c protocol.CompensationDecision
		err := w.db.View(func(v state.ReadView) error {
			target, _, err := redaction.CompensationTarget(v, w.blocks, w.policy, ob.Output, publicNow)
			if err != nil {
				return err
			}
			c = protocol.CompensationDecision{Network: target.Network, Output: target.Output, Height: target.Height, Transaction: target.Transaction, Input: target.Input}
			return nil
		})
		if err == nil {
			raw, err := c.MarshalBinary()
			if err == nil {
				code, err := w.submit(ctx, raw)
				slog.Debug("compensation decision submit", "output", ob.Output, "code", code, "error", err)
			}
		}
		if i == 31 {
			break
		} // bounded work; the cursor advances even past invalid entries
	}
	return cursor
}
