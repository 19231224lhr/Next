package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

type holdDirectWrites struct {
	store.Store
	entered, release chan struct{}
	once             sync.Once
}

func (s *holdDirectWrites) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.Store.Update(fn)
}

func clearDirect(t *testing.T, c *Collector, payments []protocol.DirectPayment) {
	t.Helper()
	if err := c.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		for _, p := range payments {
			fact, id := p.Certificate.QC.Fact, p.Tx.ID()
			o.Delete(state.Key(state.KeyOutbox, fact[:]))
			o.Delete(state.Key(state.KeyCollected, id[:]))
		}
		return o.Changes(), nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDirectInboxSubmitsAndInstallsWhilePersistenceBlocked(t *testing.T) {
	c, r, payments := directRelayFixture(t, 1)
	clearDirect(t, c, payments)
	db := &holdDirectWrites{Store: r.DB, entered: make(chan struct{}), release: make(chan struct{})}
	c.db, r.DB = db, db
	r.Early = NewDirectInbox()
	wake := make(chan protocol.SpendFactID, 8)
	r.Wake = wake
	c.NotifyPersisted = func(f protocol.SpendFactID) { wake <- f }
	submitted, installed := make(chan []byte, 16), make(chan struct{}, 16)
	r.Public = relayPublic{submit: func(_ context.Context, raw []byte) error { submitted <- bytes.Clone(raw); return nil }}
	clients := r.Members[payments[0].Tx.Body.Certifier]
	for i := range clients {
		clients[i] = relayInstaller{install: func(context.Context, protocol.DirectPayment) error { installed <- struct{}{}; return nil }}
	}
	r.Members[payments[0].Tx.Body.Certifier] = clients
	startDirectRelay(t, r)
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(db.release) }) })
	if !r.Early.Offer(payments[0]) {
		t.Fatal("inbox rejected first payment")
	}
	saved := make(chan error, 1)
	go func() { saved <- c.PersistDirect(payments[0]) }()
	<-db.entered
	select {
	case <-submitted:
	case <-time.After(time.Second):
		t.Fatal("Submit waited for gateway persistence")
	}
	select {
	case <-installed:
	case <-time.After(time.Second):
		t.Fatal("INSTALL waited for gateway persistence")
	}
	select {
	case <-saved:
		t.Fatal("store was not blocked")
	default:
	}
	release.Do(func() { close(db.release) })
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	select {
	case <-submitted:
		t.Fatal("persistence notification bypassed shared cooldown")
	case <-time.After(250 * time.Millisecond):
	}
}

func TestDirectInboxCompletedPaymentDoesNotRevive(t *testing.T) {
	c, r, payments := directRelayFixture(t, 1)
	clearDirect(t, c, payments)
	fact := payments[0].Certificate.QC.Fact
	r.Early = NewDirectInbox()
	if !r.Early.Offer(payments[0]) {
		t.Fatal("offer")
	}
	if err := r.DB.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		err := state.Put(o, state.Key(state.KeyObserved, fact[:]), true)
		return o.Changes(), err
	}); err != nil {
		t.Fatal(err)
	}
	called := make(chan struct{}, 8)
	r.Public = relayPublic{submit: func(context.Context, []byte) error { called <- struct{}{}; return nil }}
	startDirectRelay(t, r)
	if err := c.PersistDirect(payments[0]); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
		t.Fatal("completed payment resubmitted")
	case <-time.After(250 * time.Millisecond):
	}
	if err := r.DB.View(func(v state.ReadView) error {
		_, err := v.Get(state.Key(state.KeyOutbox, fact[:]))
		if !errors.Is(err, state.ErrNotFound) {
			return errors.New("outbox revived")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDirectInboxFullFallsBackToDurableScan(t *testing.T) {
	c, r, payments := directRelayFixture(t, 129)
	clearDirect(t, c, payments)
	r.Early = NewDirectInbox()
	for i, p := range payments {
		accepted := r.Early.Offer(p)
		if accepted != (i < 128) {
			t.Fatalf("offer %d accepted=%v", i, accepted)
		}
		if err := c.PersistDirect(p); err != nil {
			t.Fatal(err)
		}
	}
	seen := make(chan protocol.SpendFactID, 256)
	r.Public = relayPublic{submit: func(_ context.Context, raw []byte) error {
		p, err := protocol.DecodeDirectPayment(raw)
		if err == nil {
			seen <- p.Certificate.QC.Fact
		}
		return err
	}}
	startDirectRelay(t, r)
	unique := make(map[protocol.SpendFactID]bool)
	deadline := time.After(2 * time.Second)
	for len(unique) < len(payments) {
		select {
		case fact := <-seen:
			if unique[fact] {
				t.Fatal("early/durable duplicate")
			}
			unique[fact] = true
		case <-deadline:
			t.Fatalf("only %d delivered", len(unique))
		}
	}
}

func TestDirectInboxOwnsBytesAndRejectsAfterClose(t *testing.T) {
	_, _, payments := directRelayFixture(t, 1)
	p := payments[0]
	want, err := p.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	q := NewDirectInbox()
	if !q.Offer(p) {
		t.Fatal("offer")
	}
	p.Tx.Body.Outputs[0].Amount++
	got, ok := q.get(p.Certificate.QC.Fact)
	if !ok || !bytes.Equal(got.Certificate, want) {
		t.Fatal("caller mutation changed queued bytes")
	}
	q.close()
	if q.Offer(p) || len(q.snapshot()) != 0 {
		t.Fatal("closed inbox accepted or retained payload")
	}
}

func TestDirectInboxProcessExitBeforePersistAndFullPayloadResubmit(t *testing.T) {
	type carrier struct {
		Organization protocol.OrgConfig
		Payment      protocol.DirectPayment
	}
	identity := store.Identity{Network: "test", Role: "gateway", Node: "crash", Schema: 4}
	if dir := os.Getenv("UTXO_TEST_EARLY_EXIT"); dir != "" {
		raw, err := os.ReadFile(filepath.Join(dir, "carrier.json"))
		if err != nil {
			t.Fatal(err)
		}
		var held carrier
		if err = json.Unmarshal(raw, &held); err != nil {
			t.Fatal(err)
		}
		db, err := store.Open(filepath.Join(dir, "gateway.db"), identity)
		if err != nil {
			t.Fatal(err)
		}
		hold := &holdDirectWrites{Store: db, entered: make(chan struct{}), release: make(chan struct{})}
		var clients [4]MemberClient
		for i := range clients {
			clients[i] = relayInstaller{install: func(ctx context.Context, _ protocol.DirectPayment) error { <-ctx.Done(); return ctx.Err() }}
		}
		c, err := New(held.Organization, clients, hold)
		if err != nil {
			t.Fatal(err)
		}
		r := &Relay{DB: hold, Direct: true, Early: NewDirectInbox(), Organizations: map[protocol.Hash]protocol.OrgConfig{held.Organization.Hash(): held.Organization}, Members: map[protocol.Hash][4]MemberClient{held.Organization.Org: clients}}
		r.Trust, _ = testkit.Block("direct-relay", 1, nil, nil, nil)
		r.Public = relayPublic{submit: func(context.Context, []byte) error { os.Exit(23); return nil }}
		if !r.Early.Offer(held.Payment) {
			t.Fatal("offer")
		}
		go func() { _ = c.PersistDirect(held.Payment) }()
		<-hold.entered
		_ = r.Run(context.Background())
		t.Fatal("did not exit on early submission")
	}
	c, r, payments := directRelayFixture(t, 1)
	dir := t.TempDir()
	raw, err := json.Marshal(carrier{c.org, payments[0]})
	if err != nil {
		t.Fatal(err)
	}
	// A holder has the complete original payment. This is not an assertion that
	// the receive-only wallet can reconstruct it from an output and TXCer.
	if err = os.WriteFile(filepath.Join(dir, "carrier.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDirectInboxProcessExitBeforePersistAndFullPayloadResubmit$")
	child.Env = append(os.Environ(), "UTXO_TEST_EARLY_EXIT="+dir)
	output, err := child.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("child: %v %s", err, output)
	}
	db, err := store.Open(filepath.Join(dir, "gateway.db"), identity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	entries, err := store.Scan(db, state.Key(state.KeyOutbox), nil, 1)
	if err != nil || len(entries) != 0 {
		t.Fatal("volatile offer was mistaken for durable outbox", err)
	}
	c.db, r.DB = db, db
	sent := make(chan []byte, 8)
	r.Public = relayPublic{submit: func(_ context.Context, b []byte) error { sent <- bytes.Clone(b); return nil }}
	if err = c.PersistDirect(payments[0]); err != nil {
		t.Fatal(err)
	}
	startDirectRelay(t, r)
	want, _ := payments[0].MarshalBinary()
	select {
	case got := <-sent:
		if !bytes.Equal(got, want) {
			t.Fatal("resubmit changed payment")
		}
	case <-time.After(time.Second):
		t.Fatal("resubmission stalled")
	}
}
