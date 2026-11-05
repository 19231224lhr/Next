package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"utxo/internal/state"
	"utxo/protocol"
)

type byteRelayInstaller struct {
	relayInstaller
	installBytes func(context.Context, []byte) error
}

func (m byteRelayInstaller) InstallDirectBytes(ctx context.Context, raw []byte) error {
	return m.installBytes(ctx, raw)
}

func TestDirectInstallSharesVerifiedSnapshotBytes(t *testing.T) {
	_, r, payments := directRelayFixture(t, 1)
	fact := payments[0].Certificate.QC.Fact
	p, found, err := r.directPending(fact)
	if err != nil || !found {
		t.Fatal("missing pending", err)
	}
	expected := bytes.Clone(p.Certificate)
	prepared, err := r.prepareDirectPending(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Certificate[0] ^= 1
	// Current durability may change after scheduling. Only the captured verified
	// snapshot may supply the encoded bytes paired with the decoded payment.
	if err := r.DB.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		p.Certificate = []byte("newer unchecked bytes")
		err := state.Put(o, state.Key(state.KeyOutbox, fact[:]), p)
		return o.Changes(), err
	}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var buffers [][]byte
	members := r.Members[p.Origin]
	for i := range members {
		members[i] = byteRelayInstaller{relayInstaller: relayInstaller{install: func(context.Context, protocol.DirectPayment) error {
			t.Error("encoded target used typed path")
			return nil
		}}, installBytes: func(_ context.Context, raw []byte) error {
			mu.Lock()
			defer mu.Unlock()
			buffers = append(buffers, raw)
			return nil
		}}
	}
	r.Members[p.Origin] = members
	var wg sync.WaitGroup
	for i := range members {
		wg.Add(1)
		go func(target int) {
			defer wg.Done()
			if err := r.runDirectTask(context.Background(), directTask{fact, target}, func() (directPreparedPayment, error) { return prepared, nil }); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if len(buffers) != 4 {
		t.Fatalf("got %d installs", len(buffers))
	}
	for _, raw := range buffers {
		if !bytes.Equal(raw, expected) || &raw[0] != &buffers[0][0] {
			t.Fatal("install did not share owned verified snapshot")
		}
	}
}

func TestDirectInstallTypedFallbackAndCompletion(t *testing.T) {
	_, r, payments := directRelayFixture(t, 1)
	fact := payments[0].Certificate.QC.Fact
	p, _, _ := r.directPending(fact)
	prepared, err := r.prepareDirectPending(p)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	members := r.Members[p.Origin]
	members[0] = relayInstaller{install: func(_ context.Context, payment protocol.DirectPayment) error {
		calls++
		if payment.Tx.ID() != payments[0].Tx.ID() {
			t.Fatal("wrong typed payment")
		}
		return nil
	}}
	r.Members[p.Origin] = members
	decode := func() (directPreparedPayment, error) { return prepared, nil }
	if err := r.runDirectTask(context.Background(), directTask{fact, 0}, decode); err != nil || calls != 1 {
		t.Fatal("typed fallback failed", err)
	}
	for _, deleted := range []bool{false, true} {
		if err := r.DB.Update(func(v state.ReadView) ([]state.Change, error) {
			o := state.NewOverlay(v)
			if deleted {
				o.Delete(state.Key(state.KeyOutbox, fact[:]))
			} else {
				p.PublicComplete = true
				if err := state.Put(o, state.Key(state.KeyOutbox, fact[:]), p); err != nil {
					return nil, err
				}
			}
			return o.Changes(), nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := r.runDirectTask(context.Background(), directTask{fact, 0}, func() (directPreparedPayment, error) { t.Error("completed task decoded"); return prepared, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("completed task installed")
	}
}

func TestDirectInstallRejectsInvalidSnapshot(t *testing.T) {
	_, r, payments := directRelayFixture(t, 1)
	p, _, _ := r.directPending(payments[0].Certificate.QC.Fact)
	for _, kind := range []string{"owner", "qc", "fact"} {
		t.Run(kind, func(t *testing.T) {
			raw, _ := json.Marshal(p)
			var bad state.Outbox
			json.Unmarshal(raw, &bad)
			payment, err := protocol.DecodeDirectPayment(bad.Certificate)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "owner":
				payment.Tx.Auth[0].Signature[0] ^= 1 // Corrupt the original owner signature without invoking validating marshal.
				sig := payments[0].Tx.Auth[0].Signature[:]
				at := bytes.Index(bad.Certificate, sig)
				if at < 0 {
					t.Fatal("signature absent")
				}
				bad.Certificate[at] ^= 1
			case "qc":
				payment.Certificate.QC.Votes[0].Signature[0] ^= 1
				bad.Certificate, err = payment.MarshalBinary()
				if err != nil {
					t.Fatal(err)
				}
			case "fact":
				bad.Fact[0] ^= 1
			}
			if _, err := r.prepareDirectPending(bad); err == nil {
				t.Fatal("invalid snapshot prepared")
			}
		})
	}
}

func TestDirectPreparedPaymentDoesNotExposeCacheSnapshot(t *testing.T) {
	_, r, payments := directRelayFixture(t, 1)
	p, _, _ := r.directPending(payments[0].Certificate.QC.Fact)
	raw, _ := json.Marshal(p)
	c := newDirectOutboxCache(1, 1<<20)
	snapshot, err := c.snapshot([]byte("a"), raw)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := r.prepareDirectPending(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	prepared.installBytes[0] ^= 1
	prepared.payment.Tx.Auth[0].Signature[0] ^= 1
	again, err := c.snapshot([]byte("a"), raw)
	if err != nil || !bytes.Equal(again.Certificate, p.Certificate) {
		t.Fatal("prepared payment mutated cache", err)
	}
	if _, err := r.prepareDirectPending(again); err != nil {
		t.Fatal("cache no longer verifies", err)
	}
}

func TestDirectRelaySchedulerSharesPreparedInstall(t *testing.T) {
	_, r, payments := directRelayFixture(t, 1)
	p, _, _ := r.directPending(payments[0].Certificate.QC.Fact)
	installed := make(chan []byte, 4)
	members := r.Members[p.Origin]
	for i := range members {
		members[i] = byteRelayInstaller{
			relayInstaller: relayInstaller{install: func(context.Context, protocol.DirectPayment) error {
				t.Error("scheduler used typed path for encoded member")
				return nil
			}},
			installBytes: func(_ context.Context, raw []byte) error { installed <- raw; return nil },
		}
	}
	r.Members[p.Origin] = members
	r.Public = relayPublic{submit: func(context.Context, []byte) error { return nil }}
	startDirectRelay(t, r)
	var first []byte
	for range members {
		select {
		case raw := <-installed:
			if first == nil {
				first = raw
			}
			if !bytes.Equal(raw, p.Certificate) || &raw[0] != &first[0] {
				t.Fatal("scheduled siblings did not share verified preparation")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("scheduler failed to install every target")
		}
	}
}
