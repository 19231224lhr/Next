package gateway

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

// Present one INSTALL page while keeping the submission lane empty. Other
// reads use the real store, including each worker's durable existence check.
type refillBoundStore struct {
	store.Store
	rows   []state.Entry
	scans  atomic.Int64
	resume chan []byte
}

type refillBoundView struct {
	state.ReadView
	owner *refillBoundStore
}

func (s *refillBoundStore) View(fn func(state.ReadView) error) error {
	return s.Store.View(func(v state.ReadView) error { return fn(refillBoundView{v, s}) })
}

func (v refillBoundView) Scan(prefix, after []byte, limit int) ([]state.Entry, error) {
	call := v.owner.scans.Add(1)
	if len(after) == 0 && call != 2 {
		return nil, nil
	}
	if len(after) > 0 {
		select {
		case v.owner.resume <- bytes.Clone(after):
		default:
		}
	}
	var rows []state.Entry
	for _, row := range v.owner.rows {
		if bytes.HasPrefix(row.Key, prefix) && bytes.Compare(row.Key, after) > 0 {
			rows = append(rows, state.Entry{Key: bytes.Clone(row.Key), Value: bytes.Clone(row.Value)})
			if len(rows) == limit {
				break
			}
		}
	}
	return rows, nil
}

func TestDirectRefillStopsBeforeDecodingWhenInstallLaneFull(t *testing.T) {
	c, r, payments := directRelayFixture(t, 1)
	p := payments[0]
	key := state.Key(state.KeyOutbox, p.Certificate.QC.Fact[:])
	var raw []byte
	if err := c.db.View(func(v state.ReadView) error {
		var err error
		raw, err = v.Get(key)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	db := &refillBoundStore{
		Store: r.DB, resume: make(chan []byte, 4),
		rows: []state.Entry{{Key: key, Value: raw}, {Key: append(bytes.Clone(key), 0), Value: []byte(`{"Certificate":123}`)}},
	}
	r.DB = db
	entered := make(chan struct{}, 4)
	release := make(chan struct{}, 1)
	for org, members := range r.Members {
		for i := range members {
			members[i] = relayInstaller{install: func(ctx context.Context, _ protocol.DirectPayment) error {
				entered <- struct{}{}
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
		}
		r.Members[org] = members
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.runDirect(ctx) }()
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case err := <-done:
			t.Fatalf("INSTALL page decoded beyond available slots: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("INSTALL workers did not fill the lane")
		}
	}
	select {
	case err := <-done:
		t.Fatalf("malformed row decoded while INSTALL lane full: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("eligible malformed row was silently discarded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not resume after an INSTALL slot became available")
	}
	select {
	case after := <-db.resume:
		if !bytes.Equal(after, key) {
			t.Fatalf("cursor skipped unexamined row: %x", after)
		}
	default:
		t.Fatal("scan did not resume from its last examined row")
	}
}
