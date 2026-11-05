package gateway

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"testing"
	"time"
	"utxo/protocol"
)

type poolReplica struct {
	mu                     sync.Mutex
	vote                   protocol.PoolVote
	stored                 protocol.PoolCertificate
	approveErr, installErr error
	installs               int
	gets                   int
	approveWait            <-chan struct{}
}

func (m *poolReplica) ApprovePool(ctx context.Context, _ protocol.PoolRequest) (protocol.PoolVote, error) {
	if m.approveWait != nil {
		select {
		case <-m.approveWait:
		case <-ctx.Done():
			return protocol.PoolVote{}, ctx.Err()
		}
	}
	return m.vote, m.approveErr
}
func (m *poolReplica) InstallPool(_ context.Context, c protocol.PoolCertificate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.installErr != nil {
		return m.installErr
	}
	m.stored = c
	m.installs++
	return nil
}
func TestPoolCollectionFreshHasNoHistoricalReads(t *testing.T) {
	org, req, replicas := poolCollectionFixture()
	var members [4]PoolApprover
	for i := range members {
		members[i] = replicas[i]
	}
	c, err := CollectPool(context.Background(), org, members, req)
	if err != nil || c.Verify(org) != nil {
		t.Fatal("fresh collection", err)
	}
	for i, m := range replicas {
		m.mu.Lock()
		gets := m.gets
		m.mu.Unlock()
		if gets != 0 {
			t.Fatalf("member %d: %d historical reads", i, gets)
		}
	}
	n := 0
	for _, m := range replicas {
		m.mu.Lock()
		n += m.installs
		m.mu.Unlock()
	}
	if n < 2 {
		t.Fatalf("only %d durable copies", n)
	}
}

func TestPoolCertificateCollectionLeavesInstallationToReceiver(t *testing.T) {
	org, req, replicas := poolCollectionFixture()
	var members [4]PoolApprover
	for i := range members {
		members[i] = replicas[i]
	}
	c, err := CollectPoolCertificate(context.Background(), org, members, req)
	if err != nil || c.Verify(org) != nil {
		t.Fatal("certificate collection", err)
	}
	for i, m := range replicas {
		m.mu.Lock()
		installs := m.installs
		m.mu.Unlock()
		if installs != 0 {
			t.Fatalf("member %d installed before receiver completion", i)
		}
	}
}

func TestPoolCollectionRecoversWithWithheldApprovals(t *testing.T) {
	org, req, replicas := poolCollectionFixture()
	qc := protocol.PoolCertificate{Action: req.Action, Votes: []protocol.PoolVote{replicas[0].vote, replicas[1].vote, replicas[2].vote}}
	replicas[0].stored = qc
	blocked := make(chan struct{})
	for _, m := range replicas {
		m.approveWait = blocked
	}
	var members [4]PoolApprover
	for i := range members {
		members[i] = replicas[i]
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := CollectPool(ctx, org, members, req)
	if err != nil || c.Verify(org) != nil {
		t.Fatal("bounded recovery with withheld approvals", err)
	}
}
func TestPoolCollectionRecoversAfterInvalidVote(t *testing.T) {
	org, req, replicas := poolCollectionFixture()
	qc := protocol.PoolCertificate{Action: req.Action, Votes: []protocol.PoolVote{replicas[0].vote, replicas[1].vote, replicas[2].vote}}
	replicas[0].stored = qc
	for _, m := range replicas {
		m.vote.Signature[0] ^= 1
	}
	var members [4]PoolApprover
	for i := range members {
		members[i] = replicas[i]
	}
	c, err := CollectPool(context.Background(), org, members, req)
	if err != nil || c.Verify(org) != nil {
		t.Fatal("invalid vote recovery", err)
	}
}
func (m *poolReplica) PoolCertificate(_ context.Context, id protocol.Hash) (protocol.PoolCertificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	if m.stored.Action.ID() != id {
		return protocol.PoolCertificate{}, errors.New("missing")
	}
	return m.stored, nil
}
func poolCollectionFixture() (protocol.OrgConfig, protocol.PoolRequest, [4]*poolReplica) {
	org := protocol.OrgConfig{Network: protocol.Digest("availability-network"), Org: protocol.Digest("availability-org"), Epoch: 1}
	var keys [4]ed25519.PrivateKey
	for i := range keys {
		seed := protocol.Digest("availability-member", []byte{byte(i)})
		keys[i] = ed25519.NewKeyFromSeed(seed[:])
		copy(org.Members[i][:], keys[i].Public().(ed25519.PublicKey))
	}
	in := protocol.OutputID(protocol.Digest("availability-input"))
	a := protocol.PoolAction{Kind: protocol.PoolDeposit, Network: org.Network, Config: org.Hash(), Input: in, Evidence: protocol.Digest("creation"), Amount: 100, To: protocol.NewDescriptor(org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: org.Org}, keys[0])}
	a.Pool = protocol.PoolIdentity(a.Network, a.Config, in)
	a.Sign(keys[0])
	var replicas [4]*poolReplica
	for i := range replicas {
		replicas[i] = &poolReplica{vote: protocol.SignPool(a, uint16(i), keys[i])}
	}
	return org, protocol.PoolRequest{Action: a}, replicas
}
func TestPoolCollectionRequiresTwoDurableCopies(t *testing.T) {
	org, req, replicas := poolCollectionFixture()
	for i := 1; i < 4; i++ {
		replicas[i].installErr = errors.New("disk failed")
	}
	var members [4]PoolApprover
	for i := range members {
		members[i] = replicas[i]
	}
	if _, err := CollectPool(context.Background(), org, members, req); err == nil {
		t.Fatal("successful collection with only one durable copy")
	}
}

type contextBlockedPoolInstaller struct {
	PoolApprover
	canceled chan struct{}
}

func (m *contextBlockedPoolInstaller) InstallPool(ctx context.Context, _ protocol.PoolCertificate) error {
	<-ctx.Done()
	close(m.canceled)
	return ctx.Err()
}

func TestPoolCollectionCancelsRemainingInstallsAfterTwoAcks(t *testing.T) {
	org, req, replicas := poolCollectionFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var members [4]PoolApprover
	for i := range members {
		members[i] = replicas[i]
	}
	blocked := [2]*contextBlockedPoolInstaller{
		{PoolApprover: replicas[2], canceled: make(chan struct{})},
		{PoolApprover: replicas[3], canceled: make(chan struct{})},
	}
	members[2], members[3] = blocked[0], blocked[1]
	if _, err := CollectPool(ctx, org, members, req); err != nil {
		t.Fatal(err)
	}
	for i, m := range blocked {
		select {
		case <-m.canceled:
		case <-time.After(200 * time.Millisecond):
			cancel()
			t.Fatalf("blocked install %d survived completed collection", i)
		}
	}
}
func TestPoolCollectionRecoversQCWhenVotesUnavailable(t *testing.T) {
	org, req, replicas := poolCollectionFixture()
	qc := protocol.PoolCertificate{Action: req.Action, Votes: []protocol.PoolVote{replicas[0].vote, replicas[1].vote, replicas[2].vote}}
	replicas[0].stored = qc
	var members [4]PoolApprover
	for i := range members {
		replicas[i].approveErr = errors.New("cannot reapprove")
		members[i] = replicas[i]
	}
	c, err := CollectPool(context.Background(), org, members, req)
	if err != nil || c.Verify(org) != nil {
		t.Fatal("recover persisted QC", err)
	}
	n := 0
	for _, m := range replicas {
		m.mu.Lock()
		if m.installs > 0 {
			n++
		}
		m.mu.Unlock()
	}
	if n < 2 {
		t.Fatalf("only %d copies acknowledged", n)
	}
}
