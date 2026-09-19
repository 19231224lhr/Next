package gateway

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

type relayPublic struct {
	PublicClient
	submit func(context.Context, []byte) error
}

func (p relayPublic) Submit(ctx context.Context, raw []byte) error { return p.submit(ctx, raw) }

type relayInstaller struct {
	MemberClient
	install func(context.Context, protocol.DirectPayment) error
}

func (m relayInstaller) ApproveDirect(context.Context, protocol.DirectRequest) (protocol.DirectApproval, error) {
	return protocol.DirectApproval{}, protocol.ErrUnsupported
}
func (m relayInstaller) InstallDirect(ctx context.Context, p protocol.DirectPayment) error {
	return m.install(ctx, p)
}

func directRelayFixture(t *testing.T, count int) (*Collector, *Relay, []protocol.DirectPayment) {
	t.Helper()
	f := testkit.NewFixture("direct-relay", "org", count)
	f.EnableDirect()
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := (rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}).Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if err != nil {
		t.Fatal(err)
	}
	db := store.NewMemory()
	t.Cleanup(func() { db.Close() })
	var clients [4]MemberClient
	for i := range clients {
		clients[i] = relayInstaller{install: func(context.Context, protocol.DirectPayment) error { return nil }}
	}
	c, err := New(f.Org, clients, db)
	if err != nil {
		t.Fatal(err)
	}
	trust, _ := testkit.Block("direct-relay", 1, nil, nil, nil)
	r := &Relay{Direct: true, DB: db, Trust: trust, Organizations: map[protocol.Hash]protocol.OrgConfig{f.Org.Hash(): f.Org}, Members: map[protocol.Hash][4]MemberClient{f.Org.Org: clients}}
	var payments []protocol.DirectPayment
	for i := 0; i < count; i++ {
		tx, err := f.FastTransaction(i, uint64(i+1), policy)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := f.DirectCertificate(tx, policy)
		if err != nil {
			t.Fatal(err)
		}
		p := protocol.DirectPayment{Tx: tx, Certificate: cert}
		if err := c.PersistDirect(p); err != nil {
			t.Fatal(err)
		}
		payments = append(payments, p)
	}
	return c, r, payments
}

func startDirectRelay(t *testing.T, r *Relay) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("relay did not join its tasks")
		}
	})
}

func TestDirectRelaySubmitDoesNotWaitForInstall(t *testing.T) {
	_, r, payments := directRelayFixture(t, 1)
	entered, submitted := make(chan struct{}), make(chan struct{}, 8)
	clients := r.Members[payments[0].Tx.Body.Certifier]
	var once sync.Once
	clients[0] = relayInstaller{install: func(ctx context.Context, _ protocol.DirectPayment) error {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return ctx.Err()
	}}
	r.Members[payments[0].Tx.Body.Certifier] = clients
	r.Public = relayPublic{submit: func(context.Context, []byte) error { submitted <- struct{}{}; return nil }}
	startDirectRelay(t, r)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("INSTALL did not start")
	}
	select {
	case <-submitted:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("public submit blocked by INSTALL")
	}
}

func TestDirectRelayFillsVacanciesWithoutJoiningGroup(t *testing.T) {
	_, r, _ := directRelayFixture(t, 40)
	var once sync.Once
	completed := make(chan struct{}, 80)
	r.Public = relayPublic{submit: func(ctx context.Context, _ []byte) error {
		blocked := false
		once.Do(func() { blocked = true })
		if blocked {
			<-ctx.Done()
			return ctx.Err()
		}
		completed <- struct{}{}
		return nil
	}}
	startDirectRelay(t, r)
	deadline := time.After(time.Second)
	for i := 0; i < 39; i++ {
		select {
		case <-completed:
		case <-deadline:
			t.Fatalf("only %d other tasks completed while one was blocked", i)
		}
	}
}

func TestDirectRelaySkipsNotDueBeforeDecoding(t *testing.T) {
	_, r, payments := directRelayFixture(t, 1)
	fact := payments[0].Certificate.QC.Fact
	if err := r.DB.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		err := state.Put(o, state.Key(state.KeyOutbox, fact[:]), state.Outbox{Fact: fact, Certificate: []byte("invalid"), NextSubmitUnixNS: time.Now().Add(time.Hour).UnixNano()})
		return o.Changes(), err
	}); err != nil {
		t.Fatal(err)
	}
	called := false
	err := r.runDirectTask(context.Background(), directTask{fact, -1}, func() (protocol.DirectPayment, error) {
		called = true
		return protocol.DirectPayment{}, protocol.ErrAuth
	})
	if err != nil || called {
		t.Fatalf("not-due action decoded: called=%v err=%v", called, err)
	}
	r.MemberRelay = true
	if err := r.deliverDirect(context.Background(), state.Key(state.KeyOutbox, fact[:]), state.Outbox{Fact: fact, Certificate: []byte("invalid"), NextSubmitUnixNS: time.Now().Add(time.Hour).UnixNano()}); err != nil {
		t.Fatalf("member decoded a not-due fallback: %v", err)
	}
}

func TestMemberRelayEventuallySubmitsWithoutGateway(t *testing.T) {
	_, r, payments := directRelayFixture(t, 1)
	r.MemberRelay = true
	fact := payments[0].Certificate.QC.Fact
	due := time.Now().Add(200 * time.Millisecond).UnixNano()
	if err := r.DB.Update(func(v state.ReadView) ([]state.Change, error) {
		key := state.Key(state.KeyOutbox, fact[:])
		p, _, err := state.Load[state.Outbox](v, key)
		if err != nil {
			return nil, err
		}
		p.NextSubmitUnixNS = due
		o := state.NewOverlay(v)
		err = state.Put(o, key, p)
		return o.Changes(), err
	}); err != nil {
		t.Fatal(err)
	}
	submitted := make(chan int64, 8)
	r.Public = relayPublic{submit: func(context.Context, []byte) error { submitted <- time.Now().UnixNano(); return nil }}
	startDirectRelay(t, r) // Only a member relay is running; there is no gateway.
	select {
	case when := <-submitted:
		if when < due {
			t.Fatal("member ignored fallback deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("member never took over submission")
	}
}

func TestMemberRelayRetriesAcceptedButUncommitted(t *testing.T) {
	_, r, _ := directRelayFixture(t, 1)
	r.MemberRelay = true
	submitted := make(chan []byte, 8)
	r.Public = relayPublic{submit: func(_ context.Context, raw []byte) error {
		submitted <- bytes.Clone(raw)
		return nil // Acceptance does not mean committed; no block follows.
	}}
	startDirectRelay(t, r)
	var first []byte
	deadline := time.After(4 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case raw := <-submitted:
			if i == 0 {
				first = raw
			} else if !bytes.Equal(first, raw) {
				t.Fatal("retry changed the command")
			}
		case <-deadline:
			t.Fatal("accepted but uncommitted payment stopped retrying")
		}
	}
}

func TestDirectRelayWakeAfterPersistenceAndLostHints(t *testing.T) {
	c, r, payments := directRelayFixture(t, 2)
	if err := r.DB.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		for _, p := range payments {
			id, fact := p.Tx.ID(), p.Certificate.QC.Fact
			o.Delete(state.Key(state.KeyCollected, id[:]))
			o.Delete(state.Key(state.KeyOutbox, fact[:]))
		}
		return o.Changes(), nil
	}); err != nil {
		t.Fatal(err)
	}
	wake := make(chan protocol.SpendFactID, 1)
	r.Wake = wake
	var notified bool
	c.NotifyPersisted = func(fact protocol.SpendFactID) {
		_, found, err := r.directPending(fact)
		if !found || err != nil {
			t.Errorf("notification preceded durability: %v", err)
		}
		if !notified {
			wake <- fact
			notified = true
		} // Deliberately lose the next hint.
	}
	seen := make(chan protocol.SpendFactID, 8)
	r.Public = relayPublic{submit: func(_ context.Context, raw []byte) error {
		p, err := protocol.DecodeDirectPayment(raw)
		if err == nil {
			seen <- p.Certificate.QC.Fact
		}
		return err
	}}
	startDirectRelay(t, r)
	for _, p := range payments {
		if err := c.PersistDirect(p); err != nil {
			t.Fatal(err)
		}
	}
	unique := make(map[protocol.SpendFactID]bool)
	deadline := time.After(time.Second)
	for len(unique) < 2 {
		select {
		case fact := <-seen:
			unique[fact] = true
		case <-deadline:
			t.Fatal("lost hint lost a durable task")
		}
	}
}

func TestDirectRelayCompletionWinsOverInflightWork(t *testing.T) {
	_, r, payments := directRelayFixture(t, 1)
	fact := payments[0].Certificate.QC.Fact
	started, release := make(chan struct{}), make(chan struct{})
	r.Public = relayPublic{submit: func(ctx context.Context, _ []byte) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	t.Cleanup(cancel)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("submit never started")
	}
	// Model the atomic writes made by verified block following. The grouped
	// block-follow tests exercise the actual proof/ObserveBlock path as well.
	if err := r.DB.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		o.Delete(state.Key(state.KeyOutbox, fact[:]))
		err := state.Put(o, state.Key(state.KeyObserved, fact[:]), true)
		return o.Changes(), err
	}); err != nil {
		t.Fatal(err)
	}
	r.forgetInstall(fact)
	close(release)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("tasks did not drain")
	}
	if err := r.DB.View(func(v state.ReadView) error {
		_, err := v.Get(state.Key(state.KeyOutbox, fact[:]))
		if !errors.Is(err, state.ErrNotFound) {
			return errors.New("completed outbox revived")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.installAck(fact, 0)
	if len(r.installs) != 0 {
		t.Fatal("late ACK revived completed scheduling state")
	}
}
