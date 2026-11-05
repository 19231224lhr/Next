package wallet

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"testing"
	"time"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

type poolInstallStub struct {
	started chan struct{}
	release <-chan struct{}
	err     error
	once    sync.Once
}
type contextBlockedWalletInstaller struct{ canceled chan struct{} }

func (m *contextBlockedWalletInstaller) InstallPool(ctx context.Context, _ protocol.PoolCertificate) error {
	<-ctx.Done()
	close(m.canceled)
	return ctx.Err()
}

type countedPoolStore struct {
	store.Store
	updates int
	fail    error
}

func (s *countedPoolStore) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	s.updates++
	if s.fail != nil {
		return s.fail
	}
	return s.Store.Update(fn)
}

func (m *poolInstallStub) InstallPool(ctx context.Context, _ protocol.PoolCertificate) error {
	if m.started != nil {
		m.once.Do(func() { close(m.started) })
	}
	if m.release != nil {
		select {
		case <-m.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.err
}

func poolWalletFixture(t *testing.T) (*Wallet, protocol.PoolCertificate, protocol.PoolRequest) {
	t.Helper()
	f := testkit.NewFixture("pool-ready-wallet", "org", 1)
	seed := protocol.Digest("pool-ready-owner")
	ownerKey := ed25519.NewKeyFromSeed(seed[:])
	receiver := protocol.NewDescriptor(f.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: f.Org.Org}, ownerKey)
	f.Genesis.Wallets = append(f.Genesis.Wallets, state.WalletBinding{Owner: receiver.Owner, Organization: f.Org.Org})
	db := store.NewMemory()
	t.Cleanup(func() { _ = db.Close() })
	w, err := NewWithGenesis(db, f.Org.Network, receiver.Owner, []protocol.OrgConfig{f.Org}, f.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	parent := protocol.PoolAction{Kind: protocol.PoolTransfer, Network: f.Org.Network, Config: f.Org.Hash(), Pool: protocol.Digest("pool-ready-pool"), Sequence: 1, Previous: protocol.Digest("pool-ready-deposit"), Amount: 100, To: receiver}
	parent.Sign(f.Owner)
	c := protocol.PoolCertificate{Action: parent}
	for i := 0; i < 3; i++ {
		c.Votes = append(c.Votes, protocol.SignPool(parent, uint16(i), f.Keys[i]))
	}
	if err := c.Verify(f.Org); err != nil {
		t.Fatal(err)
	}
	next := protocol.PoolAction{Kind: protocol.PoolExit, Network: parent.Network, Config: parent.Config, Pool: parent.Pool, Sequence: 2, Previous: parent.ID(), Amount: parent.Amount, To: receiver}
	next.Sign(ownerKey)
	return w, c, protocol.PoolRequest{Action: next, Parent: &c}
}

func TestPoolSavedReceiptIsNotReadyUntilTwoInstalls(t *testing.T) {
	w, c, next := poolWalletFixture(t)
	release := make(chan struct{})
	var members [4]PoolInstaller
	for i := range members {
		members[i] = &poolInstallStub{started: make(chan struct{}), release: release}
	}
	done := make(chan error, 1)
	go func() { done <- w.CompletePool(context.Background(), c, members) }()
	for _, m := range members {
		<-m.(*poolInstallStub).started
	}
	deadline := time.Now().Add(time.Second)
	for {
		var found bool
		if err := w.db.View(func(v state.ReadView) (e error) {
			_, found, e = state.Load[protocol.PoolCertificate](v, PoolReceiptKey(c.Action.Pool))
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("receipt not saved during blocked installation")
		}
		time.Sleep(time.Millisecond)
	}
	if err := w.SavePoolRequest(next); !errors.Is(err, protocol.ErrAuth) {
		t.Fatalf("staged receipt spent during install: %v", err)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("completion stalled")
	}
	if err := w.SavePoolRequest(next); err != nil {
		t.Fatal("confirmed receipt unusable:", err)
	}
}

func TestPoolOneInstallAndWalletErrorNeverReady(t *testing.T) {
	w, c, next := poolWalletFixture(t)
	var members [4]PoolInstaller
	for i := range members {
		members[i] = &poolInstallStub{err: errors.New("disk failed")}
	}
	members[0] = &poolInstallStub{}
	if err := w.CompletePool(context.Background(), c, members); err == nil {
		t.Fatal("one install reported ready")
	}
	if err := w.SavePoolRequest(next); !errors.Is(err, protocol.ErrAuth) {
		t.Fatalf("one install spent: %v", err)
	}
	bad := c
	bad.Action.To.Owner[0] ^= 1
	for i := range members {
		members[i] = &poolInstallStub{}
	}
	if err := w.CompletePool(context.Background(), bad, members); !errors.Is(err, protocol.ErrAuth) {
		t.Fatalf("wallet error lost: %v", err)
	}
	if err := w.SavePoolRequest(next); !errors.Is(err, protocol.ErrAuth) {
		t.Fatalf("invalid receipt spent: %v", err)
	}
}

func TestPoolCompletionCancelsRemainingInstallsAfterTwoAcks(t *testing.T) {
	w, c, _ := poolWalletFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blocked := [2]*contextBlockedWalletInstaller{{canceled: make(chan struct{})}, {canceled: make(chan struct{})}}
	members := [4]PoolInstaller{&poolInstallStub{}, &poolInstallStub{}, blocked[0], blocked[1]}
	if err := w.CompletePool(ctx, c, members); err != nil {
		t.Fatal(err)
	}
	for i, m := range blocked {
		select {
		case <-m.canceled:
		case <-time.After(200 * time.Millisecond):
			cancel()
			t.Fatalf("blocked install %d survived completed wallet completion", i)
		}
	}
}

func TestPoolDurableWalletFailurePropagates(t *testing.T) {
	w, c, next := poolWalletFixture(t)
	want := errors.New("wallet disk failed")
	counted := &countedPoolStore{Store: w.db, fail: want}
	w.db = counted
	var members [4]PoolInstaller
	for i := range members {
		members[i] = &poolInstallStub{}
	}
	if err := w.CompletePool(context.Background(), c, members); !errors.Is(err, want) {
		t.Fatalf("wallet error = %v", err)
	}
	counted.fail = nil
	if err := w.SavePoolRequest(next); !errors.Is(err, protocol.ErrAuth) {
		t.Fatalf("failed receipt spent: %v", err)
	}
}

func TestPoolDuplicateConfirmationAvoidsWalletWrite(t *testing.T) {
	w, c, _ := poolWalletFixture(t)
	counted := &countedPoolStore{Store: w.db}
	w.db = counted
	var members [4]PoolInstaller
	for i := range members {
		members[i] = &poolInstallStub{}
	}
	if err := w.CompletePool(context.Background(), c, members); err != nil {
		t.Fatal(err)
	}
	first := counted.updates
	if first != 1 {
		t.Fatalf("initial receipt writes = %d", first)
	}
	if err := w.CompletePool(context.Background(), c, members); err != nil {
		t.Fatal(err)
	}
	if counted.updates != first {
		t.Fatalf("duplicate added wallet writes: %d -> %d", first, counted.updates)
	}
}

func TestPoolRestartRequiresTwoFreshConfirmations(t *testing.T) {
	w, c, next := poolWalletFixture(t)
	var members [4]PoolInstaller
	for i := range members {
		members[i] = &poolInstallStub{}
	}
	if err := w.CompletePool(context.Background(), c, members); err != nil {
		t.Fatal(err)
	}
	org := w.orgs[c.Action.Config]
	restarted, err := New(w.db, w.network, w.owner, []protocol.OrgConfig{org})
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.SavePoolRequest(next); !errors.Is(err, protocol.ErrAuth) {
		t.Fatalf("restart spent receipt before confirmation: %v", err)
	}
	if err = restarted.CompletePool(context.Background(), c, members); err != nil {
		t.Fatal(err)
	}
	if err = restarted.SavePoolRequest(next); err != nil {
		t.Fatal("restart recovery:", err)
	}
}

func TestPoolNewerReceiptInvalidatesExactHead(t *testing.T) {
	w, c, next := poolWalletFixture(t)
	var members [4]PoolInstaller
	for i := range members {
		members[i] = &poolInstallStub{}
	}
	if err := w.CompletePool(context.Background(), c, members); err != nil {
		t.Fatal(err)
	}
	f := testkit.NewFixture("pool-ready-wallet", "org", 1)
	newer := c.Action
	newer.Sequence++
	newer.Previous = c.Action.ID()
	newer.Sign(f.Owner)
	qc := protocol.PoolCertificate{Action: newer}
	for i := 0; i < 3; i++ {
		qc.Votes = append(qc.Votes, protocol.SignPool(newer, uint16(i), f.Keys[i]))
	}
	if err := w.ReceivePool(qc); err != nil {
		t.Fatal(err)
	}
	if err := w.SavePoolRequest(next); !errors.Is(err, protocol.ErrAuth) {
		t.Fatalf("older parent spent: %v", err)
	}
	if err := w.CompletePool(context.Background(), c, members); !errors.Is(err, protocol.ErrAuth) {
		t.Fatalf("older confirmation restored readiness: %v", err)
	}
	if err := w.CompletePool(context.Background(), qc, members); err != nil {
		t.Fatal(err)
	}
	conflict := newer
	conflict.Previous = protocol.Digest("conflicting previous")
	conflict.Sign(f.Owner)
	bad := protocol.PoolCertificate{Action: conflict}
	for i := 0; i < 3; i++ {
		bad.Votes = append(bad.Votes, protocol.SignPool(conflict, uint16(i), f.Keys[i]))
	}
	if err := w.ReceivePool(bad); !errors.Is(err, protocol.ErrAuth) {
		t.Fatalf("same-sequence conflict accepted: %v", err)
	}
	if err := w.ReceivePool(c); err != nil {
		t.Fatal("older receipt should be ignored:", err)
	}
	if err := w.ReceivePool(qc); err != nil {
		t.Fatal("duplicate receipt should be idempotent:", err)
	}
	// The current receipt remains ready after conflicting and duplicate deliveries.
	w.poolMu.Lock()
	ready := w.poolReady[qc.Action.Pool]
	w.poolMu.Unlock()
	if ready != poolCertificateIdentity(qc) {
		t.Fatal("current head lost readiness")
	}
	seed := protocol.Digest("pool-ready-owner")
	ownerKey := ed25519.NewKeyFromSeed(seed[:])
	next.Action.Sequence = qc.Action.Sequence + 1
	next.Action.Previous = qc.Action.ID()
	next.Action.Sign(ownerKey)
	next.Parent = &qc
	if err := w.SavePoolRequest(next); err != nil {
		t.Fatal("current confirmed head unusable:", err)
	}
}

func TestPoolConcurrentNewerReceiptCannotBeMarkedReadyByOlderCompletion(t *testing.T) {
	w, c, next := poolWalletFixture(t)
	release := make(chan struct{})
	var members [4]PoolInstaller
	for i := range members {
		members[i] = &poolInstallStub{started: make(chan struct{}), release: release}
	}
	done := make(chan error, 1)
	go func() { done <- w.CompletePool(context.Background(), c, members) }()
	for _, m := range members {
		<-m.(*poolInstallStub).started
	}
	f := testkit.NewFixture("pool-ready-wallet", "org", 1)
	newer := c.Action
	newer.Sequence++
	newer.Previous = c.Action.ID()
	newer.Sign(f.Owner)
	qc := protocol.PoolCertificate{Action: newer}
	for i := 0; i < 3; i++ {
		qc.Votes = append(qc.Votes, protocol.SignPool(newer, uint16(i), f.Keys[i]))
	}
	if err := w.ReceivePool(qc); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, protocol.ErrAuth) {
		t.Fatalf("older completion marked newer head ready: %v", err)
	}
	if err := w.SavePoolRequest(next); !errors.Is(err, protocol.ErrAuth) {
		t.Fatalf("old parent spent after newer arrival: %v", err)
	}
}

func TestPoolSameActionDifferentQCRequiresExactConfirmation(t *testing.T) {
	w, c, next := poolWalletFixture(t)
	var members [4]PoolInstaller
	for i := range members {
		members[i] = &poolInstallStub{}
	}
	if err := w.CompletePool(context.Background(), c, members); err != nil {
		t.Fatal(err)
	}
	f := testkit.NewFixture("pool-ready-wallet", "org", 1)
	other := protocol.PoolCertificate{Action: c.Action, Votes: []protocol.PoolVote{c.Votes[0], c.Votes[1], protocol.SignPool(c.Action, 3, f.Keys[3])}}
	if err := w.ReceivePool(other); err != nil {
		t.Fatal(err)
	}
	if err := w.SavePoolRequest(next); !errors.Is(err, protocol.ErrAuth) {
		t.Fatalf("old exact QC remained ready after replacement: %v", err)
	}
	if err := w.CompletePool(context.Background(), other, members); err != nil {
		t.Fatal(err)
	}
	next.Parent = &other
	if err := w.SavePoolRequest(next); err != nil {
		t.Fatal("replacement QC unusable:", err)
	}
}
