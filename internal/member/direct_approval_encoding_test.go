package member_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestDirectStoredApprovalEncodingAndReplay(t *testing.T) {
	x := newReclaimLab(t, 300)
	tx := x.tx(t, 0, 31, false)
	req := protocol.DirectRequest{Tx: tx}
	raw, err := req.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := protocol.DecodeDirectRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, err := x.m[0].ApproveDirectBytes(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	fact := a.Summary.Fact()
	want := state.Approval{Fact: fact, Direct: &decoded.Tx, Admission: a.Summary.Admission, DirectParents: decoded.InputCertificates}
	for _, allocation := range want.Admission {
		want.Debits = append(want.Debits, state.Debit{Key: allocation.Key, Cap: allocation.Cap, Worker: 0})
	}
	wantBytes, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := x.db[0].View(func(v state.ReadView) error {
		got, err := v.Get(state.Key(state.KeyApproval, fact[:]))
		if err == nil && !bytes.Equal(got, wantBytes) {
			t.Fatal("stored approval encoding changed")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before, err := store.Scan(x.db[0], nil, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := x.m[0].ApproveDirectBytes(context.Background(), raw)
	if err != nil || !reflect.DeepEqual(a, replay) {
		t.Fatal("replay changed approval", err)
	}
	after, err := store.Scan(x.db[0], nil, nil, 1000)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("replay changed state", err)
	}
}

func TestDirectConcurrentApprovalReservationRemainsAtomic(t *testing.T) {
	for _, kind := range []protocol.ResourceKind{protocol.ResourceCAL, protocol.ResourceFUEL} {
		t.Run(map[protocol.ResourceKind]string{protocol.ResourceCAL: "CAL", protocol.ResourceFUEL: "FUEL"}[kind], func(t *testing.T) {
			x := newReclaimLab(t, 300)
			txs := []protocol.FastTx{x.tx(t, 0, 32, false), x.tx(t, 1, 33, false)}
			vector, err := rules.PrepareDirectVector(txs[0], x.p)
			if err != nil {
				t.Fatal(err)
			}
			var limited protocol.Allocation
			for _, a := range vector {
				if a.Key.Kind == kind {
					limited = a
				}
			}
			if limited.Cap == 0 {
				t.Fatal("missing tested resource")
			}
			if err := x.db[0].Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				key := state.SliceKey(limited.Key, 0)
				s, found, err := state.Load[state.Slice](o, key)
				if err != nil || !found {
					t.Fatal("missing slice", err)
				}
				s.Available = limited.Cap
				if err := state.Put(o, key, s); err != nil {
					return nil, err
				}
				return o.Changes(), nil
			}); err != nil {
				t.Fatal(err)
			}
			type result struct {
				index int
				err   error
			}
			results := make(chan result, 2)
			start := make(chan struct{})
			for i, tx := range txs {
				go func(i int, tx protocol.FastTx) {
					<-start
					_, err := x.m[0].ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx})
					results <- result{i, err}
				}(i, tx)
			}
			close(start)
			winner, loser := -1, -1
			for range txs {
				r := <-results
				if r.err == nil {
					winner = r.index
				} else if errors.Is(r.err, rules.ErrLimited) {
					loser = r.index
				} else {
					t.Fatal(r.err)
				}
			}
			if winner < 0 || loser < 0 || winner == loser {
				t.Fatal("expected exactly one approval")
			}
			if err := x.db[0].View(func(v state.ReadView) error {
				for _, a := range vector {
					s, _, err := state.Load[state.Slice](v, state.SliceKey(a.Key, 0))
					if err != nil {
						return err
					}
					if s.Reserved != a.Cap {
						t.Fatal("failed request leaked reservation", a.Key.Kind, s)
					}
				}
				spend, _, err := state.Load[state.Spend](v, rules.DirectSpendKey(txs[loser].Body.Inputs[0].Output, 0))
				if err != nil {
					return err
				}
				if spend.Candidate != (protocol.SpendFactID{}) {
					t.Fatal("failed request leaked lock")
				}
				loserVector, err := rules.PrepareDirectVector(txs[loser], x.p)
				if err != nil {
					return err
				}
				fact := protocol.SummaryFor(txs[loser], loserVector).Fact()
				_, found, err := state.Load[state.Approval](v, state.Key(state.KeyApproval, fact[:]))
				if found {
					t.Fatal("failed request stored approval")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
