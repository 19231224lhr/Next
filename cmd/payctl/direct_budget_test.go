package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
	"utxo/internal/redaction"
	"utxo/internal/rules"
	"utxo/protocol"
)

func TestRepairReleaseWaitsForAllPhysicalStores(t *testing.T) {
	urls := make([]string, 4)
	calls := [4]int{}
	for i := range urls {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v3/obligations/"+protocol.Hash(protocol.OutputID{1}).String() {
				_ = json.NewEncoder(w).Encode(rules.DirectObligation{Deadline: time.Now().Unix() - 1})
				return
			}
			calls[i]++
			_ = json.NewEncoder(w).Encode(redaction.Observation{Committed: true, Materialized: i != 3 || calls[i] > 1, IdentityStable: true, BytesChanged: true, CommitHeight: 5, TargetHeight: 2, Revision: 1})
		}))
		defer s.Close()
		urls[i] = s.URL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var trial repairTrial
	if err := waitRepair(ctx, http.DefaultClient, urls, protocol.OutputID{1}, &trial); err != nil {
		t.Fatal(err)
	}
	if calls[3] < 2 || len(trial.Nodes) != 4 {
		t.Fatalf("released on Commit alone: %+v", trial)
	}
	for _, n := range trial.Nodes {
		if n.CommittedUnixNS == 0 || n.MaterializedUnixNS < n.CommittedUnixNS {
			t.Fatal("missing completion observations")
		}
	}
}

func TestRepairReleaseNeverFallsBackToTimer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var trial repairTrial
	if err := waitRepair(ctx, http.DefaultClient, []string{"http://127.0.0.1:1"}, protocol.OutputID{1}, &trial); !errors.Is(err, context.Canceled) {
		t.Fatalf("must remain withheld: %v", err)
	}
}

func TestRepairSelectionNestedAndBalanced(t *testing.T) {
	zero := repairSelection(6000, 0, 23)
	one := repairSelection(6000, 1, 23)
	five := repairSelection(6000, 5, 23)
	if !reflect.DeepEqual(five, repairSelection(6000, 5, 23)) || reflect.DeepEqual(five, repairSelection(6000, 5, 24)) {
		t.Fatal("selection must be repeatable and seed-dependent")
	}
	for base := 0; base < 6000; base += 100 {
		n1, n5 := 0, 0
		for i := base; i < base+100; i++ {
			if zero[i] || (one[i] && !five[i]) {
				t.Fatal("groups are not nested")
			}
			if one[i] {
				n1++
			}
			if five[i] {
				n5++
			}
		}
		if n1 != 1 || n5 != 5 {
			t.Fatalf("block %d: %d/%d", base, n1, n5)
		}
	}
}

func TestBudgetReleaseFixedDeadlineAndRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	at := time.Now().Add(20 * time.Millisecond)
	attempts := 0
	err := budgetRelease(ctx, at, func(context.Context) error {
		if time.Now().Before(at) {
			t.Fatal("parent published early")
		}
		if attempts == 1 {
			return errors.New("temporary transport failure")
		}
		return nil
	}, func() { attempts++ })
	if err != nil || attempts != 2 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestBudgetParentDoesNotUseChildContext(t *testing.T) {
	experiment, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	child, stopChild := context.WithCancel(experiment)
	stopChild()
	if child.Err() == nil {
		t.Fatal("child should fail")
	}
	called := false
	if e := budgetRelease(experiment, time.Now().Add(10*time.Millisecond), func(context.Context) error { called = true; return nil }, func() {}); e != nil || !called {
		t.Fatalf("parent stopped with child: %v", e)
	}
}
