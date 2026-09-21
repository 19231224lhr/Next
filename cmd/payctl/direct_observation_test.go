package main

import (
	"errors"
	"testing"
)

func TestFinalObservationFirstAndNewCommit(t *testing.T) {
	var o finalObservation
	calls := 0
	ready := false
	q := func() (bool, error) { calls++; return ready, nil }
	if ok, e := o.Check(0, q); e != nil || ok {
		t.Fatal(ok, e)
	}
	for range 10 {
		if _, e := o.Check(0, q); e != nil {
			t.Fatal(e)
		}
	}
	if calls != 1 {
		t.Fatal("repeated read at unchanged height", calls)
	}
	ready = true
	if ok, e := o.Check(1, q); e != nil || !ok {
		t.Fatal("missed final block", ok, e)
	}
	var first finalObservation
	if ok, e := first.Check(1, q); e != nil || !ok {
		t.Fatal("missed Final before certificate", ok, e)
	}
}
func TestFinalObservationCommitDuringReadAndReadError(t *testing.T) {
	var o finalObservation
	h := int64(1)
	calls := 0
	q := func() (bool, error) { calls++; h = 2; return false, nil }
	if _, e := o.Check(h, q); e != nil {
		t.Fatal(e)
	}
	if _, e := o.Check(h, q); e != nil {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatal("commit during read was lost")
	}
	fail := errors.New("read failed")
	if _, e := o.Check(3, func() (bool, error) { return false, fail }); !errors.Is(e, fail) {
		t.Fatal(e)
	}
	if ok, e := o.Check(3, func() (bool, error) { return true, nil }); e != nil || !ok {
		t.Fatal("failed read suppressed retry", ok, e)
	}
}

func TestMemberObservationRemembersOnlySuccessfulChecks(t *testing.T) {
	var o memberObservation
	calls := [4]int{}
	ready := false
	query := func(i int) (bool, error) { calls[i]++; return i != 1 || ready, nil }
	if ok, e := o.Check(4, query); e != nil || ok {
		t.Fatal("early completion", ok, e)
	}
	if calls != [4]int{1, 1, 0, 0} {
		t.Fatal(calls)
	}
	boom := errors.New("query failed")
	if _, e := o.Check(4, func(i int) (bool, error) { return false, boom }); !errors.Is(e, boom) {
		t.Fatal(e)
	}
	ready = true
	if ok, e := o.Check(4, query); e != nil || !ok {
		t.Fatal("did not finish", ok, e)
	}
	if calls != [4]int{1, 2, 1, 1} {
		t.Fatal("repeated completed member", calls)
	}
	if ok, e := o.Check(4, func(int) (bool, error) { t.Fatal("repeated completed observation"); return false, nil }); e != nil || !ok {
		t.Fatal(ok, e)
	}
}
