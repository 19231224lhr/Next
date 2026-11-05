package main

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestE7ReceiptOnlyAfterSuccessfulReceive(t *testing.T) {
	r := newE7Receipts()
	var n atomic.Int32
	if _, e := r.accept(1, func() (e7Ack, error) { return e7Ack{}, errors.New("bad signature") }); e == nil {
		t.Fatal("invalid receipt accepted")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, e := r.accept(1, func() (e7Ack, error) { n.Add(1); return e7Ack{Index: 1, ReceiveNS: 17}, nil })
			if e != nil || a.ReceiveNS != 17 {
				t.Errorf("receipt: %v %v", a, e)
			}
		}()
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Fatalf("continued %d times", n.Load())
	}
}
