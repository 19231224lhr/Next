package poollab

import (
	"context"
	"sync"
	"testing"
	"time"
	"utxo/internal/committee"
	"utxo/internal/gateway"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

func newLab(t *testing.T, persistent bool) *Lab {
	t.Helper()
	dir := ""
	if persistent {
		dir = t.TempDir()
	}
	l, e := New(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(l.Close)
	return l
}
func deposit(t *testing.T, l *Lab) protocol.PoolCertificate {
	t.Helper()
	r := l.DepositRequest()
	if e := l.Wallets[0].SavePoolRequest(r); e != nil {
		t.Fatal(e)
	}
	c, e := l.Collect(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	if e = l.Publish(c); e != nil {
		t.Fatal(e)
	}
	return c
}

func collect(t *testing.T, l *Lab, r protocol.PoolRequest) protocol.PoolCertificate {
	t.Helper()
	c, e := l.Collect(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestPoolClosedFundsLifecycle(t *testing.T) {
	r, e := Run(context.Background(), 100, "")
	if e != nil {
		t.Fatal(e)
	}
	if r.DepositCAL != 100 || r.EscrowDuringTransfersCAL != 100 || r.EscrowAfterExitCAL != 0 || r.ExitCoinCAL != 100 || !r.SubsequentOrdinaryPaymentApproved || r.PublicBlocks != 2 {
		t.Fatalf("bad lifecycle: %+v", r)
	}
	if r.MemberCALReservedDuringTransfers != ([4]uint64{}) {
		t.Fatalf("pool reserved ordinary CAL: %v", r.MemberCALReservedDuringTransfers)
	}
}

func TestPoolSerialLifecycleUsesSameReadyBoundary(t *testing.T) {
	r, err := RunSerial(context.Background(), 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Transfers != 3 || r.ExitCoinCAL != 100 || len(r.LatencyMS) != 3 {
		t.Fatalf("bad serial result: %+v", r)
	}
}

func TestOrdinaryCapitalBaseline(t *testing.T) {
	r, e := RunOrdinary(context.Background(), 100, "")
	if e != nil {
		t.Fatal(e)
	}
	if r.CertifiedFaceAmountCAL != 10000 || r.PublicBlocks != 0 || len(r.LatencyMS) != 100 {
		t.Fatalf("bad ordinary baseline: %+v", r)
	}
	var total uint64
	for _, reserved := range r.MemberCALReserved {
		if reserved > 10000 || reserved%100 != 0 {
			t.Fatalf("invalid member reservation: %v", r.MemberCALReserved)
		}
		total += reserved
	}
	// Every certificate has three votes, each reserving 100 on its own replica.
	if total < 30000 || total > 40000 {
		t.Fatalf("missing certified reservations: %v", r.MemberCALReserved)
	}
}

func TestPoolDirectDepositShareSpendLock(t *testing.T) {
	for _, poolFirst := range []bool{true, false} {
		name := "ordinary-first"
		if poolFirst {
			name = "pool-first"
		}
		t.Run(name, func(t *testing.T) {
			l := newLab(t, false)
			tx, e := l.OrdinaryTemplate(0, 1)
			if e != nil {
				t.Fatal(e)
			}
			r := l.DepositRequest()
			ctx := context.Background()
			for i := 0; i < 3; i++ {
				if poolFirst {
					if _, e = l.Members[i].ApprovePool(ctx, r); e != nil {
						t.Fatal(e)
					}
					if _, e = l.Members[i].ApproveDirect(ctx, protocol.DirectRequest{Tx: tx}); e == nil {
						t.Fatal("ordinary double-spend approved after deposit")
					}
				} else {
					if _, e = l.Members[i].ApproveDirect(ctx, protocol.DirectRequest{Tx: tx}); e != nil {
						t.Fatal(e)
					}
					if _, e = l.Members[i].ApprovePool(ctx, r); e == nil {
						t.Fatal("deposit double-spend approved after ordinary payment")
					}
				}
			}
			if poolFirst {
				if e = l.Wallets[0].SavePoolRequest(r); e != nil {
					t.Fatal(e)
				}
				if e = l.Wallets[0].SaveDirectRequest(protocol.DirectRequest{Tx: tx}); e == nil {
					t.Fatal("wallet double reservation")
				}
			} else {
				if e = l.Wallets[0].SaveDirectRequest(protocol.DirectRequest{Tx: tx}); e != nil {
					t.Fatal(e)
				}
				if e = l.Wallets[0].SavePoolRequest(r); e == nil {
					t.Fatal("wallet double reservation")
				}
			}
		})
	}
}

func TestPoolMustObservePublicDeposit(t *testing.T) {
	l := newLab(t, false)
	d := collect(t, l, l.DepositRequest())
	r := l.Next(d, protocol.PoolTransfer, 0, 1)
	if _, e := l.Collect(context.Background(), r); e == nil {
		t.Fatal("unpublished deposit spendable")
	}
	if e := l.Publish(d); e != nil {
		t.Fatal(e)
	}
	collect(t, l, r)
}

func TestPoolPersistentVoteAndPendingLock(t *testing.T) {
	l := newLab(t, true)
	d := deposit(t, l)
	r := l.Next(d, protocol.PoolTransfer, 0, 1)
	v, e := l.Clients[0].ApprovePool(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	if e = l.RestartMember(0); e != nil {
		t.Fatal(e)
	}
	again, e := l.Clients[0].ApprovePool(context.Background(), r)
	if e != nil || v != again {
		t.Fatalf("lost persisted approval: %v", e)
	}
	conflict := l.Next(d, protocol.PoolExit, 0, 0)
	if _, e = l.Clients[0].ApprovePool(context.Background(), conflict); e == nil {
		t.Fatal("restart released competing exit lock")
	}
}

func TestPoolStaleOwnerAndReceiptsCannotRedeem(t *testing.T) {
	l := newLab(t, false)
	d := deposit(t, l)
	transfer := collect(t, l, l.Next(d, protocol.PoolTransfer, 0, 1))
	if e := l.Wallets[1].ReceivePool(transfer); e != nil {
		t.Fatal(e)
	}
	if e := l.WalletDB[1].View(func(v state.ReadView) error {
		_, found, e := state.Load[wallet.DirectCoin](v, wallet.DirectCoinKey(transfer.Action.OutputID(), 0))
		if found {
			t.Fatal("internal transfer created an ordinary coin before exit")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := l.Collect(context.Background(), l.Next(d, protocol.PoolExit, 0, 0)); e == nil {
		t.Fatal("stale owner obtained exit quorum")
	}
	raw, _ := transfer.MarshalBinary()
	if e := l.Engine.Check(raw); e == nil {
		t.Fatal("transfer receipt accepted by public Check")
	}
	if e := l.PublicDB.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, e := l.Engine.ExecuteAt(v, raw, committee.BlockContext{Height: 2, Time: time.Unix(1700000002, 0)})
		return tr.Changes, e
	}); e == nil {
		t.Fatal("transfer receipt publicly redeemed")
	}
	exit := collect(t, l, l.Next(transfer, protocol.PoolExit, 1, 1))
	if e := l.Publish(exit); e != nil {
		t.Fatal(e)
	}
	if e := l.Publish(exit); e != nil {
		t.Fatal("identical exit replay must be idempotent:", e)
	}
	balance, e := l.Escrow(d.Action.Pool)
	if e != nil || balance != 0 {
		t.Fatalf("exit escrow=%d err=%v", balance, e)
	}
	e = l.WalletDB[1].View(func(v state.ReadView) error {
		coin, found, e := state.Load[wallet.DirectCoin](v, wallet.DirectCoinKey(exit.Action.OutputID(), 0))
		if !found || coin.Output.Amount != 100 || coin.Final != exit.Action.CreationFact() {
			t.Fatalf("bad exit coin: %+v", coin)
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	e = l.PublicDB.View(func(v state.ReadView) error {
		spent, found, e := state.Load[state.Spend](v, rules.DirectSpendKey(d.Action.Input, 0))
		if !found || spent.Consumed != protocol.SpendFactID(d.Action.ID()) {
			t.Fatal("deposit input not consumed")
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = l.SpendExit(context.Background(), exit, 1); e != nil {
		t.Fatal(e)
	}
}

func TestPoolMinorityDepositFollowsOrdinaryWinner(t *testing.T) {
	l := newLab(t, false)
	ctx := context.Background()
	deposit := l.DepositRequest()
	if _, e := l.Clients[0].ApprovePool(ctx, deposit); e != nil {
		t.Fatal(e)
	}
	tx, e := l.OrdinaryTemplate(0, 11)
	if e != nil {
		t.Fatal(e)
	}
	cert := protocol.OutputCertificate{}
	for i := 1; i < 4; i++ {
		a, e := l.Members[i].ApproveDirect(ctx, protocol.DirectRequest{Tx: tx})
		if e != nil {
			t.Fatal(e)
		}
		cert.Summary = a.Summary
		cert.QC.Fact = a.Summary.Fact()
		cert.QC.Votes = append(cert.QC.Votes, a.Vote)
	}
	if e = cert.Verify(l.Fixture.Org); e != nil {
		t.Fatal(e)
	}
	raw, e := (protocol.DirectPayment{Tx: tx, Certificate: cert}).Submission().MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	if e = l.PublishBytes(raw); e != nil {
		t.Fatal("minority pool vote blocked public ordinary winner:", e)
	}
	if _, e = l.Clients[0].ApprovePool(ctx, deposit); e == nil {
		t.Fatal("superseded deposit vote resurrected after public ordinary winner")
	}
}

func TestPoolMinorityOrdinaryFollowsDepositAndReleasesDebits(t *testing.T) {
	l := newLab(t, false)
	ctx := context.Background()
	tx, e := l.OrdinaryTemplate(0, 12)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = l.Members[0].ApproveDirect(ctx, protocol.DirectRequest{Tx: tx}); e != nil {
		t.Fatal(e)
	}
	var calKey protocol.ResourceKey
	for _, g := range l.Fixture.Genesis.Grants {
		if g.Key.Kind == protocol.ResourceCAL {
			calKey = g.Key
		}
	}
	var before state.Slice
	if e = l.MemberDB[0].View(func(v state.ReadView) error {
		var e error
		before, _, e = state.Load[state.Slice](v, state.SliceKey(calKey, 0))
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if before.Reserved != 100 {
		t.Fatalf("ordinary vote did not reserve CAL: %+v", before)
	}
	peers := l.Clients
	peers[0] = nil
	qc, e := gateway.CollectPool(ctx, l.Fixture.Org, peers, l.DepositRequest())
	if e != nil {
		t.Fatal(e)
	}
	if e = l.Publish(qc); e != nil {
		t.Fatal(e)
	}
	if e = l.MemberDB[0].View(func(v state.ReadView) error {
		after, _, e := state.Load[state.Slice](v, state.SliceKey(calKey, 0))
		if after.Reserved != 0 || after.Available != before.Available+before.Reserved {
			t.Fatalf("minority ordinary CAL reservation leaked: before=%+v after=%+v", before, after)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = l.Members[0].ApproveDirect(ctx, protocol.DirectRequest{Tx: tx}); e == nil {
		t.Fatal("superseded ordinary vote resurrected")
	}
	collect(t, l, l.Next(qc, protocol.PoolTransfer, 0, 1))
}

func TestPoolTransferExitConcurrentQuorumExclusion(t *testing.T) {
	for attempt := 0; attempt < 10; attempt++ {
		l := newLab(t, false)
		d := deposit(t, l)
		requests := [2]protocol.PoolRequest{l.Next(d, protocol.PoolTransfer, 0, 1), l.Next(d, protocol.PoolExit, 0, 0)}
		var wg sync.WaitGroup
		var errs [2]error
		start := make(chan struct{})
		for i := range requests {
			wg.Add(1)
			go func(i int) { defer wg.Done(); <-start; _, errs[i] = l.Collect(context.Background(), requests[i]) }(i)
		}
		close(start)
		wg.Wait()
		if errs[0] == nil && errs[1] == nil {
			t.Fatal("both transfer and exit obtained quorum")
		}
		// A 2:2 split is allowed to lack a QC; never claim contention liveness.
	}
}

func TestPoolMinorityConflictCatchesUpFromFullQC(t *testing.T) {
	l := newLab(t, false)
	d := deposit(t, l)
	wrong := l.Next(d, protocol.PoolTransfer, 0, 2)
	if _, e := l.Clients[0].ApprovePool(context.Background(), wrong); e != nil {
		t.Fatal(e)
	}
	available := l.Clients
	available[0] = nil
	right := l.Next(d, protocol.PoolTransfer, 0, 1)
	qc, e := gateway.CollectPool(context.Background(), l.Fixture.Org, available, right)
	if e != nil {
		t.Fatal(e)
	}
	next := l.Next(qc, protocol.PoolTransfer, 1, 2)
	if _, e = l.Clients[0].ApprovePool(context.Background(), next); e != nil {
		t.Fatal("minority vote prevented valid QC catchup:", e)
	}
	collect(t, l, next)
}

func TestPoolUnavailableMembers(t *testing.T) {
	for _, missing := range []int{1, 2} {
		t.Run(string(rune('0'+missing))+"-unavailable", func(t *testing.T) {
			l := newLab(t, false)
			d := deposit(t, l)
			for i := 0; i < missing; i++ {
				l.Servers[i].Close()
			}
			_, e := l.Collect(context.Background(), l.Next(d, protocol.PoolTransfer, 0, 1))
			if missing == 1 && e != nil {
				t.Fatal(e)
			}
			if missing == 2 && e == nil {
				t.Fatal("two-member quorum accepted")
			}
		})
	}
}

type fixedVote struct{ v protocol.PoolVote }

func (f fixedVote) ApprovePool(context.Context, protocol.PoolRequest) (protocol.PoolVote, error) {
	return f.v, nil
}
func (f fixedVote) InstallPool(context.Context, protocol.PoolCertificate) error {
	return protocol.ErrAuth
}
func (f fixedVote) PoolCertificate(context.Context, protocol.Hash) (protocol.PoolCertificate, error) {
	return protocol.PoolCertificate{}, protocol.ErrAuth
}

type failInstall struct{ gateway.PoolApprover }

func (f failInstall) InstallPool(context.Context, protocol.PoolCertificate) error {
	return protocol.ErrUnsupported
}

func TestPoolRequiresTwoCertificateInstallAcknowledgments(t *testing.T) {
	l := newLab(t, false)
	d := deposit(t, l)
	peers := l.Clients
	for i := 1; i < 4; i++ {
		peers[i] = failInstall{peers[i]}
	}
	qc, e := gateway.CollectPool(context.Background(), l.Fixture.Org, peers, l.Next(d, protocol.PoolTransfer, 0, 1))
	if qc.Verify(l.Fixture.Org) != nil {
		t.Fatal("expected valid vote QC before installation failure")
	}
	if e == nil {
		t.Fatal("collector reported success with only one durable certificate copy")
	}
}

func TestPoolFullCertificateRecoveryAfterRecipientCrash(t *testing.T) {
	l := newLab(t, true)
	ctx := context.Background()
	d := deposit(t, l)
	wrong := l.Next(d, protocol.PoolTransfer, 0, 2)
	if _, e := l.Clients[3].ApprovePool(ctx, wrong); e != nil {
		t.Fatal(e)
	}
	request := l.Next(d, protocol.PoolTransfer, 0, 1)
	peers := l.Clients
	peers[3] = nil
	qc, e := gateway.CollectPool(ctx, l.Fixture.Org, peers, request)
	if e != nil {
		t.Fatal(e)
	}
	// Do not call ReceivePool: the recipient crashes before saving its receipt.
	if e = l.WalletDB[1].View(func(v state.ReadView) error {
		_, found, e := state.Load[protocol.PoolCertificate](v, wallet.PoolReceiptKey(qc.Action.Pool))
		if found {
			t.Fatal("receipt unexpectedly already stored")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	l.Servers[2].Close() // The third signer now withholds its vote and full QC.
	if _, e = l.Clients[3].ApprovePool(ctx, request); e == nil {
		t.Fatal("counterexample requires fourth member's conflicting vote")
	}
	for i := 0; i < 2; i++ {
		if e = l.RestartMember(i); e != nil {
			t.Fatal(e)
		}
	}
	found := 0
	for i := 0; i < 2; i++ {
		stored, e := transport.NewMemberClient(l.Servers[i].URL).PoolCertificate(ctx, qc.Action.ID())
		if e == nil {
			if stored.Verify(l.Fixture.Org) != nil || stored.Action.ID() != qc.Action.ID() {
				t.Fatal("invalid recovered full certificate")
			}
			found++
		}
	}
	if found == 0 {
		t.Fatal("no honest durable full-QC copy survived restart")
	}
	// Retry must recover that QC; the two remaining original votes alone cannot
	// satisfy a three-vote quorum while member 3 retains its conflicting vote.
	recovered, e := l.Collect(ctx, request)
	if e != nil {
		t.Fatal("full-QC recovery failed:", e)
	}
	if recovered.Action.ID() != qc.Action.ID() {
		t.Fatal("recovery changed the action")
	}
	if e = l.Wallets[1].ReceivePool(recovered); e != nil {
		t.Fatal(e)
	}
	collect(t, l, l.Next(recovered, protocol.PoolTransfer, 1, 2))
}
func TestPoolAuthenticityAndDuplicateVotes(t *testing.T) {
	l := newLab(t, false)
	d := deposit(t, l)
	r := l.Next(d, protocol.PoolTransfer, 0, 1)
	badNetwork := r
	badNetwork.Action.Network = protocol.Digest("OTHER_NETWORK")
	badNetwork.Action.Sign(l.Keys[0])
	if _, e := l.Collect(context.Background(), badNetwork); e == nil {
		t.Fatal("wrong network accepted")
	}
	badConfig := r
	badConfig.Action.Config = protocol.Digest("OTHER_CONFIG")
	badConfig.Action.Sign(l.Keys[0])
	if _, e := l.Collect(context.Background(), badConfig); e == nil {
		t.Fatal("wrong config accepted")
	}
	v := protocol.SignPool(r.Action, 0, l.Fixture.Keys[0])
	c := protocol.PoolCertificate{Action: r.Action, Votes: []protocol.PoolVote{v, v, v}}
	if c.Verify(l.Fixture.Org) == nil {
		t.Fatal("duplicate signer accepted")
	}
	peers := [4]gateway.PoolApprover{fixedVote{v}, fixedVote{v}, fixedVote{v}, fixedVote{v}}
	if _, e := gateway.CollectPool(context.Background(), l.Fixture.Org, peers, r); e == nil {
		t.Fatal("gateway counted duplicate or misplaced members")
	}
	wrong := protocol.SignPool(r.Action, 1, l.Fixture.Keys[0])
	peers = [4]gateway.PoolApprover{fixedVote{wrong}, fixedVote{wrong}, fixedVote{wrong}, fixedVote{wrong}}
	if _, e := gateway.CollectPool(context.Background(), l.Fixture.Org, peers, r); e == nil {
		t.Fatal("wrong member key accepted")
	}
	collect(t, l, r)
}
