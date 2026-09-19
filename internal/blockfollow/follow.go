// Package blockfollow reads each committed height once and atomically advances
// the reader's own state. It never requests payment-specific committee proofs.
package blockfollow

import (
	"bytes"
	"context"
	"log/slog"
	"time"
	"utxo/finality"
	"utxo/internal/requesttrace"
	"utxo/internal/state"
	"utxo/internal/store"
)

var cursorKey = state.Key(120)

type Cursor struct {
	Height int64
	Hash   []byte
}
type Apply func(*state.Overlay, finality.VerifiedBlock) error
type Source interface {
	Block(context.Context, int64) (finality.BlockData, error)
}

func Height(db store.Store) (height int64, err error) {
	err = db.View(func(v state.ReadView) error { c, _, e := state.Load[Cursor](v, cursorKey); height = c.Height; return e })
	return
}
func Commit(db store.Store, b finality.VerifiedBlock, apply Apply) error {
	requesttrace.Consensus.Mark("follow_update_requested", "height", b.Height())
	err := db.Update(func(v state.ReadView) ([]state.Change, error) {
		requesttrace.Consensus.Mark("follow_update_started", "height", b.Height())
		c, _, err := state.Load[Cursor](v, cursorKey)
		if err != nil {
			return nil, err
		}
		if b.Height() == c.Height && bytes.Equal(b.Hash(), c.Hash) {
			return nil, nil
		}
		if b.Height() != c.Height+1 || c.Height > 0 && !bytes.Equal(b.Previous(), c.Hash) {
			return nil, finality.ErrProof
		}
		o := state.NewOverlay(v)
		requesttrace.Consensus.Mark("follow_apply_start", "height", b.Height())
		if err = apply(o, b); err != nil {
			return nil, err
		}
		requesttrace.Consensus.Mark("follow_apply_done", "height", b.Height())
		if err = state.Put(o, cursorKey, Cursor{b.Height(), b.Hash()}); err != nil {
			return nil, err
		}
		return o.Changes(), nil
	})
	if err == nil {
		requesttrace.Consensus.Mark("follow_committed", "height", b.Height())
	}
	return err
}
func Run(ctx context.Context, db store.Store, s Source, t finality.Trust, apply Apply) error {
	if t.Validate() != nil {
		return finality.ErrProof
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var waiting int64
	for {
		height, err := Height(db)
		if err != nil {
			return err
		}
		if waiting != height+1 {
			waiting = height + 1
			requesttrace.Consensus.Mark("follow_wait_start", "height", waiting)
		}
		p, err := s.Block(ctx, height+1)
		if err == nil {
			requesttrace.Consensus.Mark("follow_verify_start", "height", height+1)
			var b finality.VerifiedBlock
			b, err = finality.VerifyBlock(t, p)
			if err != nil {
				return err
			}
			requesttrace.Consensus.Mark("follow_verify_done", "height", height+1)
			if err = Commit(db, b, apply); err != nil {
				return err
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Start joins the follower before its database is closed. An apply error cancels
// the service context instead of silently leaving quota permanently occupied.
func Start(ctx context.Context, cancel context.CancelFunc, db store.Store, s Source, t finality.Trust, apply Apply) func() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := Run(ctx, db, s, t, apply); err != nil {
			slog.Error("block follower stopped", "error", err)
			cancel()
		}
	}()
	return func() { cancel(); <-done }
}
