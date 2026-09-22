package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
