package gateway

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type countedDirectScanStore struct {
	store.Store
	scans   atomic.Int64
	scanned chan time.Time
}

type countedDirectScanView struct {
	state.ReadView
	state.ScanView
	owner *countedDirectScanStore
}

func (s *countedDirectScanStore) View(fn func(state.ReadView) error) error {
	return s.Store.View(func(v state.ReadView) error { return fn(countedDirectScanView{v, v.(state.ScanView), s}) })
}

func (v countedDirectScanView) Scan(prefix, after []byte, limit int) ([]state.Entry, error) {
	v.owner.scans.Add(1)
	v.owner.scanned <- time.Now()
	return v.ScanView.Scan(prefix, after, limit)
}

func TestDirectRelayExhaustedScanDoesNotRestartOnCompletion(t *testing.T) {
	_, r, _ := directRelayFixture(t, 1)
	db := &countedDirectScanStore{Store: r.DB, scanned: make(chan time.Time, 128)}
	r.DB = db
	// No INSTALL targets: both durable scans exhaust, leaving one public action.
	r.Members = make(map[protocol.Hash][4]MemberClient)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	r.Public = relayPublic{submit: func(ctx context.Context, _ []byte) error {
		entered <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	start := time.Now()
	startDirectRelay(t, r)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("submit never started")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-db.scanned:
		case <-time.After(time.Second):
			t.Fatal("initial lanes did not scan")
		}
	}
	// Completion creates a two-second submit cooldown. It must not reopen an
	// already exhausted scan; the normal 100ms ticker still revisits durability.
	close(release)
	select {
	case at := <-db.scanned:
		if at.Sub(start) < 90*time.Millisecond {
			t.Fatal("completion restarted exhausted cooling scan")
		}
	case <-time.After(25 * time.Millisecond):
	}
	select {
	case <-db.scanned:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("periodic durable recovery stopped")
	}
}

func TestDirectRelayCapacityBlockedInstallTargetsAreRevisited(t *testing.T) {
	_, r, payments := directRelayFixture(t, 2)
	installed := make(chan directTask, 8)
	release := make(chan struct{})
	origin := payments[0].Tx.Body.Certifier
	members := r.Members[origin]
	first := payments[0].Certificate.QC.Fact
	var calls atomic.Int64
	for i := range members {
		target := i
		members[i] = relayInstaller{install: func(ctx context.Context, p protocol.DirectPayment) error {
			task := directTask{p.Certificate.QC.Fact, target}
			installed <- task
			if calls.Add(1) <= 4 {
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}}
	}
	r.Members[origin] = members
	r.Public = relayPublic{submit: func(context.Context, []byte) error { return nil }}
	startDirectRelay(t, r)
	seen := make(map[directTask]bool)
	for i := 0; i < 4; i++ {
		select {
		case task := <-installed:
			seen[task] = true
		case <-time.After(time.Second):
			t.Fatal("install slots did not fill")
		}
	}
	close(release)
	deadline := time.After(time.Second)
	for len(seen) < 8 {
		select {
		case task := <-installed:
			seen[task] = true
		case <-deadline:
			t.Fatalf("only %d targets reached; first fact %v", len(seen), first)
		}
	}
}
