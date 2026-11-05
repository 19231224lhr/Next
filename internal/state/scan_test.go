package state_test

import (
	"errors"
	"reflect"
	"testing"
	"utxo/internal/state"
	"utxo/internal/store"
)

type brokenScan struct{}

func (brokenScan) Get([]byte) ([]byte, error) { return nil, state.ErrNotFound }
func (brokenScan) Scan([]byte, []byte, int) ([]state.Entry, error) {
	return nil, errors.New("scan failed")
}
func TestOverlayScanMergesNestedBoundedQueues(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	if e := db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		for _, key := range []string{"p1", "p2", "p3", "p4", "q1"} {
			o.Set([]byte(key), []byte(key))
		}
		return o.Changes(), nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := db.View(func(v state.ReadView) error {
		a := state.NewOverlay(v)
		a.Delete([]byte("p1"))
		a.Delete([]byte("p2"))
		a.Set([]byte("p3"), []byte("overwrite"))
		a.Set([]byte("p0"), []byte("new"))
		b := state.NewOverlay(a)
		b.Delete([]byte("p4"))
		b.Set([]byte("p35"), []byte("nested"))
		b.Set([]byte("q0"), []byte("wrong prefix"))
		rows, e := b.Scan([]byte("p"), []byte("p0"), 2)
		if e != nil {
			return e
		}
		want := []state.Entry{{Key: []byte("p3"), Value: []byte("overwrite")}, {Key: []byte("p35"), Value: []byte("nested")}}
		if !reflect.DeepEqual(rows, want) {
			t.Fatalf("got %+v", rows)
		}
		rows[0].Value[0] = '!'
		again, e := b.Scan([]byte("p"), nil, 1)
		if e != nil {
			return e
		}
		if string(again[0].Key) != "p0" {
			t.Fatal("unsorted", again)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := state.NewOverlay(brokenScan{}).Scan([]byte("p"), nil, 1); e == nil {
		t.Fatal("scan error hidden")
	}
}
