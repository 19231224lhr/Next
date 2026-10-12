// Package poollab exercises the opt-in pool through real member HTTP handlers,
// durable stores, public execution, and authenticated block followers. Its block
// headers use testkit laboratory keys; this is not a live consensus benchmark.
package poollab

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	abci "github.com/cometbft/cometbft/abci/types"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/committee"
	"utxo/internal/gateway"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

const chain = "internal-pool-lab-v1"

type Lab struct {
	Fixture     testkit.Fixture
	Settings    rules.DirectSettings
	Policy      rules.DirectPolicy
	Keys        [3]ed25519.PrivateKey
	Descriptors [3]protocol.ReceiveDescriptor
	Members     [4]*member.Member
	MemberDB    [4]store.Store
	Servers     [4]*httptest.Server
	Clients     [4]gateway.PoolApprover
	Wallets     [3]*wallet.Wallet
	WalletDB    [3]store.Store
	Engine      *committee.Engine
	PublicDB    store.Store
	Directory   string
	height      int64
	previous    []byte
}

// New uses memory when directory is empty, otherwise synchronous Bolt stores.
// A directory must be fresh, except RestartMember explicitly reopens its store.
func New(directory string) (l *Lab, err error) {
	l = &Lab{Directory: directory, Fixture: testkit.NewFixture(chain, "organization", 1)}
	defer func() {
		if err != nil {
			l.Close()
		}
	}()
	l.Fixture.EnableDirect()
	l.Keys[0] = l.Fixture.Owner
	for i := 1; i < 3; i++ {
		seed := protocol.Digest("POOL_LAB_OWNER", []byte{byte(i)})
		l.Keys[i] = ed25519.NewKeyFromSeed(seed[:])
	}
	for i, k := range l.Keys {
		l.Descriptors[i] = protocol.NewDescriptor(l.Fixture.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: l.Fixture.Org.Org}, k)
		l.Fixture.Genesis.Wallets = append(l.Fixture.Genesis.Wallets, state.WalletBinding{Owner: l.Descriptors[i].Owner, Organization: l.Fixture.Org.Org})
		if i > 0 {
			l.Fixture.Genesis.Grants = append(l.Fixture.Genesis.Grants, state.Grant{ID: protocol.Digest("POOL_LAB_POLICY_GRANT", []byte{byte(i)}), Organization: l.Fixture.Org.Hash(), Key: protocol.ResourceKey{Kind: protocol.ResourcePolicy, Account: protocol.Digest("POOL_LAB_POLICY", []byte{byte(i)}), Version: 1}, Amount: 1_000_000_000, Subject: l.Descriptors[i].Owner})
		}
	}
	_, source, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../crypto/chameleon/testdata/rsa2048.pem"))
	if err != nil {
		return l, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return l, fmt.Errorf("invalid laboratory RSA fixture")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return l, err
	}
	l.Settings = rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	l.Policy, err = l.Settings.Policy(l.Fixture.Schedule, []protocol.OrgConfig{l.Fixture.Org})
	if err != nil {
		return l, err
	}
	l.PublicDB, err = l.open("public")
	if err != nil {
		return l, err
	}
	l.Engine, err = committee.NewEngine(committee.EngineConfig{EnableInternalPools: true, Network: l.Fixture.Org.Network, Organizations: []protocol.OrgConfig{l.Fixture.Org}, Schedule: l.Fixture.Schedule, Genesis: l.Fixture.Genesis, Direct: &l.Settings, Accounts: []committee.GenesisAccount{{Owner: l.Fixture.Org.Org, Asset: protocol.AssetCAL, Balance: 1_000_000_000}, {Owner: l.Fixture.Org.Org, Asset: protocol.AssetFUEL, Balance: 1_000_000_000}}}, l.PublicDB)
	if err != nil {
		return l, err
	}
	for i := range l.Members {
		if err = l.startMember(i); err != nil {
			return l, err
		}
	}
	for i := range l.Wallets {
		l.WalletDB[i], err = l.open(fmt.Sprintf("wallet-%d", i))
		if err != nil {
			return l, err
		}
		l.Wallets[i], err = wallet.NewWithGenesis(l.WalletDB[i], l.Fixture.Org.Network, l.Descriptors[i].Owner, []protocol.OrgConfig{l.Fixture.Org}, l.Fixture.Genesis)
		if err != nil {
			return l, err
		}
	}
	return l, nil
}
func (l *Lab) open(name string) (store.Store, error) {
	if l.Directory == "" {
		return store.NewMemory(), nil
	}
	return store.Open(filepath.Join(l.Directory, name+".db"), store.Identity{Network: chain, Role: "pool-lab", Node: name, Schema: 1})
}
func (l *Lab) startMember(i int) error {
	db, err := l.open(fmt.Sprintf("member-%d", i))
	if err != nil {
		return err
	}
	l.MemberDB[i] = db
	trust, _ := testkit.Block(chain, 1, nil, nil, nil)
	m, err := member.New(member.Config{EnableInternalPools: true, Organization: l.Fixture.Org, Index: uint16(i), Key: l.Fixture.Keys[i], Peers: []protocol.OrgConfig{l.Fixture.Org}, Schedule: l.Fixture.Schedule, Workers: 1, Direct: &l.Settings, Committee: trust}, db, l.Fixture.Genesis)
	if err != nil {
		return err
	}
	l.Members[i] = m
	l.Servers[i] = httptest.NewServer(transport.MemberHandler(m, 16, 4))
	l.Clients[i] = transport.NewMemberClient(l.Servers[i].URL)
	return nil
}
func (l *Lab) RestartMember(i int) error {
	if l.Directory == "" {
		return fmt.Errorf("restart requires persistent directory")
	}
	l.Servers[i].Close()
	if err := l.MemberDB[i].Close(); err != nil {
		return err
	}
	return l.startMember(i)
}
func (l *Lab) Close() {
	for _, s := range l.Servers {
		if s != nil {
			s.Close()
		}
	}
	for _, d := range l.MemberDB {
		if d != nil {
			_ = d.Close()
		}
	}
	for _, d := range l.WalletDB {
		if d != nil {
			_ = d.Close()
		}
	}
	if l.PublicDB != nil {
		_ = l.PublicDB.Close()
	}
}
func (l *Lab) DepositRequest() protocol.PoolRequest {
	g := l.Fixture.Genesis.Outputs[0]
	a := protocol.PoolAction{Kind: protocol.PoolDeposit, Network: l.Fixture.Org.Network, Config: l.Fixture.Org.Hash(), Input: g.ID, Evidence: g.Fact, Amount: g.Output.Amount, To: l.Descriptors[0]}
	a.Pool = protocol.PoolIdentity(a.Network, a.Config, a.Input)
	a.Sign(l.Keys[0])
	return protocol.PoolRequest{Action: a}
}
func (l *Lab) Next(parent protocol.PoolCertificate, kind uint8, from, to int) protocol.PoolRequest {
	p := parent.Action
	a := protocol.PoolAction{Kind: kind, Network: p.Network, Config: p.Config, Pool: p.Pool, Sequence: p.Sequence + 1, Previous: p.ID(), Amount: p.Amount, To: l.Descriptors[to]}
	a.Sign(l.Keys[from])
	return protocol.PoolRequest{Action: a, Parent: &parent}
}
func (l *Lab) Collect(ctx context.Context, r protocol.PoolRequest) (protocol.PoolCertificate, error) {
	return gateway.CollectPool(ctx, l.Fixture.Org, l.Clients, r)
}
func (l *Lab) CollectReady(ctx context.Context, r protocol.PoolRequest, receiver int, serial bool) (protocol.PoolCertificate, error) {
	c, err := gateway.CollectPoolCertificate(ctx, l.Fixture.Org, l.Clients, r)
	if err != nil {
		return c, err
	}
	var members [4]wallet.PoolInstaller
	for i := range members {
		members[i] = l.Clients[i]
	}
	if serial {
		err = l.Wallets[receiver].CompletePoolSerial(ctx, c, members)
	} else {
		err = l.Wallets[receiver].CompletePool(ctx, c, members)
	}
	return c, err
}
func (l *Lab) Publish(c protocol.PoolCertificate) error {
	raw, err := c.MarshalBinary()
	if err != nil {
		return err
	}
	return l.PublishBytes(raw)
}
func (l *Lab) PublishBytes(raw []byte) error {
	if err := l.Engine.Check(raw); err != nil {
		return fmt.Errorf("public check: %w", err)
	}
	next := l.height + 1
	var executionData []byte
	err := l.PublicDB.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, e := l.Engine.ExecuteAt(v, raw, committee.BlockContext{Height: next, Time: time.Unix(1700000000+next, 0)})
		executionData = tr.Data
		return tr.Changes, e
	})
	if err != nil {
		return fmt.Errorf("public execute: %w", err)
	}
	trust, data := testkit.Block(chain, next, l.previous, [][]byte{raw}, []*abci.ExecTxResult{{Code: 0, Data: executionData}})
	block, err := finality.VerifyBlock(trust, data)
	if err != nil {
		return fmt.Errorf("block verification: %w", err)
	}
	for i, m := range l.Members {
		if err = blockfollow.Commit(l.MemberDB[i], block, m.PrepareBlock); err != nil {
			return fmt.Errorf("member %d follow: %w", i, err)
		}
	}
	for i, w := range l.Wallets {
		if err = blockfollow.Commit(l.WalletDB[i], block, w.PrepareBlock); err != nil {
			return fmt.Errorf("wallet %d follow: %w", i, err)
		}
	}
	l.height = next
	l.previous = block.Hash()
	return nil
}
func (l *Lab) Escrow(pool protocol.Hash) (balance uint64, err error) {
	err = l.PublicDB.View(func(v state.ReadView) error {
		var e error
		balance, _, e = state.Load[uint64](v, rules.PoolEscrowKey(pool))
		return e
	})
	return
}

type Report struct {
	Mode                              string    `json:"mode"`
	Storage                           string    `json:"storage"`
	Transfers                         int       `json:"transfers"`
	WalletOwners                      int       `json:"wallet_owners"`
	Members                           int       `json:"members"`
	DepositCAL                        uint64    `json:"deposit_cal"`
	EscrowDuringTransfersCAL          uint64    `json:"escrow_during_transfers_cal"`
	MemberCALReservedDuringTransfers  [4]uint64 `json:"member_cal_reserved_during_transfers"`
	EscrowAfterExitCAL                uint64    `json:"escrow_after_exit_cal"`
	ExitCoinCAL                       uint64    `json:"exit_coin_cal"`
	SubsequentOrdinaryPaymentApproved bool      `json:"subsequent_ordinary_payment_approved"`
	LatencyMS                         []float64 `json:"transfer_latency_ms"`
	P50MS                             float64   `json:"p50_ms"`
	P95MS                             float64   `json:"p95_ms"`
	PublicBlocks                      int64     `json:"public_blocks"`
	Finality                          string    `json:"finality"`
}

func Run(ctx context.Context, n int, directory string) (Report, error) {
	return run(ctx, n, directory, false)
}
func RunSerial(ctx context.Context, n int, directory string) (Report, error) {
	return run(ctx, n, directory, true)
}
func run(ctx context.Context, n int, directory string, serial bool) (Report, error) {
	report := Report{Mode: "opt-in internal pool; loopback HTTP", Storage: "memory", Transfers: n, WalletOwners: 3, Members: 4, Finality: "testkit signed laboratory headers verified with finality.VerifyBlock; no running validator network"}
	if serial {
		report.Mode += "; serial durable completion"
	} else {
		report.Mode += "; overlapped durable completion"
	}
	if directory != "" {
		report.Storage = "synchronous Bolt"
	}
	if n < 1 {
		return report, fmt.Errorf("transfers must be positive")
	}
	l, err := New(directory)
	if err != nil {
		return report, err
	}
	defer l.Close()
	req := l.DepositRequest()
	if err = l.Wallets[0].SavePoolRequest(req); err != nil {
		return report, err
	}
	head, err := l.Collect(ctx, req)
	if err != nil {
		return report, err
	}
	if err = l.Publish(head); err != nil {
		return report, err
	}
	report.DepositCAL = head.Action.Amount
	owner := 0
	for i := 0; i < n; i++ {
		to := (owner + 1) % 3
		r := l.Next(head, protocol.PoolTransfer, owner, to)
		start := time.Now()
		if err = l.Wallets[owner].SavePoolRequest(r); err != nil {
			return report, err
		}
		head, err = l.CollectReady(ctx, r, to, serial)
		if err != nil {
			return report, err
		}
		report.LatencyMS = append(report.LatencyMS, float64(time.Since(start).Nanoseconds())/1e6)
		balance, e := l.Escrow(head.Action.Pool)
		if e != nil {
			return report, e
		}
		if balance != 100 {
			return report, fmt.Errorf("escrow changed: %d", balance)
		}
		report.EscrowDuringTransfersCAL = balance
		owner = to
	}
	report.MemberCALReservedDuringTransfers, err = l.CALReservations()
	if err != nil {
		return report, err
	}
	r := l.Next(head, protocol.PoolExit, owner, owner)
	if err = l.Wallets[owner].SavePoolRequest(r); err != nil {
		return report, err
	}
	exit, err := l.Collect(ctx, r)
	if err != nil {
		return report, err
	}
	if err = l.Publish(exit); err != nil {
		return report, err
	}
	report.EscrowAfterExitCAL, err = l.Escrow(exit.Action.Pool)
	if err != nil {
		return report, err
	}
	var coin wallet.DirectCoin
	err = l.WalletDB[owner].View(func(v state.ReadView) error {
		var found bool
		var e error
		coin, found, e = state.Load[wallet.DirectCoin](v, wallet.DirectCoinKey(exit.Action.OutputID(), 0))
		if e == nil && !found {
			return fmt.Errorf("exit coin absent")
		}
		return e
	})
	if err != nil {
		return report, err
	}
	report.ExitCoinCAL = coin.Output.Amount
	if err = l.SpendExit(ctx, exit, owner); err != nil {
		return report, err
	}
	report.SubsequentOrdinaryPaymentApproved = true
	sorted := append([]float64(nil), report.LatencyMS...)
	sort.Float64s(sorted)
	report.P50MS = sorted[(len(sorted)-1)*50/100]
	report.P95MS = sorted[(len(sorted)-1)*95/100]
	report.PublicBlocks = l.height
	return report, nil
}

// SpendExit proves the ordinary coin is usable through wallet reservation and
// three independent member approvals, rather than merely asserting a balance.
func (l *Lab) OrdinaryTemplate(owner int, nonce uint64) (protocol.FastTx, error) {
	f := l.Fixture
	f.Genesis.Grants = nil
	for _, g := range l.Fixture.Genesis.Grants {
		if g.Key.Kind != protocol.ResourcePolicy || g.Subject == l.Descriptors[owner].Owner {
			f.Genesis.Grants = append(f.Genesis.Grants, g)
		}
	}
	return f.FastTransaction(0, nonce, l.Policy)
}

func (l *Lab) SpendExit(ctx context.Context, exit protocol.PoolCertificate, owner int) error {
	tx, err := l.OrdinaryTemplate(owner, 99)
	if err != nil {
		return err
	}
	b := tx.Body
	b.Subject = l.Descriptors[owner].Owner
	b.Inputs = []protocol.Input{{Kind: protocol.FinalInput, Output: exit.Action.OutputID(), Evidence: exit.Action.CreationFact()}}
	out := protocol.Output{Asset: protocol.AssetCAL, Amount: exit.Action.Amount, Recipient: exit.Action.To}
	b.Outputs = []protocol.Output{out}
	tx, err = protocol.NewFastTx(b, []protocol.InputClaim{{Output: out}}, l.Policy.Key)
	if err != nil {
		return err
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), l.Keys[owner])}
	req := protocol.DirectRequest{Tx: tx}
	if err = l.Wallets[owner].SaveDirectRequest(req); err != nil {
		return fmt.Errorf("exit coin wallet spend: %w", err)
	}
	cert := protocol.OutputCertificate{}
	for i := 0; i < 3; i++ {
		client := transport.NewMemberClient(l.Servers[i].URL)
		a, e := client.ApproveDirect(ctx, req)
		if e != nil {
			return fmt.Errorf("exit coin ordinary approval: %w", e)
		}
		cert.Summary = a.Summary
		cert.QC.Fact = a.Summary.Fact()
		cert.QC.Votes = append(cert.QC.Votes, a.Vote)
	}
	return cert.Verify(l.Fixture.Org)
}
