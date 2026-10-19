package main

// c3-fees is an opt-in real-service experiment client. Every certificate and
// wallet result crosses the same verification boundary as an ordinary payment.
import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/budgetprobe"
	"utxo/internal/committee"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type c3FeePayment struct {
	ServiceFeeCAL, ExpectedReceiveCAL                                     uint64
	Fact                                                                  protocol.SpendFactID
	Tx                                                                    protocol.TxID
	Output                                                                protocol.OutputID
	FeeInput                                                              state.OriginOutput
	FeeChange                                                             wallet.DirectCoin
	FeeRefund                                                             *wallet.DirectCoin
	RefundChecked                                                         bool
	FeeObservedHeight                                                     int64
	FinalCoin                                                             wallet.DirectCoin
	FeeNet                                                                uint64
	RequestRaw, CertificateRaw, SubmissionRaw                             []byte
	BuildNS, ReceiptNS, CertifiedOffsetNS, SubmitOffsetNS, PublicOffsetNS int64
	SubmitAttempts                                                        int
	SubmitAttemptLog                                                      []c3SubmitAttempt
	CertificateAttempts                                                   int
	MembersClosed                                                         [4]member.DirectStatus
	ExecutionHeight                                                       int64
	ExecutionIndex                                                        int
	MissingInputs, RecoveredOutputs                                       []uint32
}
type c3FeeStage struct {
	Name          string
	OffsetNS      int64
	Source, Child *committee.C3Snapshot
	Obligation    *rules.DirectObligation
}
type c3FeePair struct {
	Index                     int
	Source, Child             c3FeePayment
	Stages                    []c3FeeStage
	ReplayBefore, ReplayAfter *committee.C3Snapshot
	Repair                    *repairTrial `json:",omitempty"`
	ReplayError               string       `json:",omitempty"`
	Replay                    c3ReplayEvidence
	Error                     string `json:",omitempty"`
}
type c3FeeMember struct {
	URL      string
	Snapshot budgetprobe.Snapshot
}
type c3FeeSample struct {
	OffsetNS int64
	Pools    []committee.C3PoolSnapshot
	Members  []c3FeeMember
	Error    string `json:",omitempty"`
}
type c3FeeBlock struct {
	Height       int64
	Hash         []byte
	Transactions []finality.ExecutedTx
	Executions   []c3FeeExecution
}
type c3FeeExecution struct {
	Index          int
	Kind           string
	Fact           protocol.SpendFactID
	Tx             protocol.TxID
	Decision       *protocol.CompensationDecision `json:",omitempty"`
	Result         protocol.ExecutionResult
	Effects        []protocol.RepairEffect
	HistoryApplied *bool `json:",omitempty"`
}

func c3DecodeExecutions(network protocol.Hash, txs []finality.ExecutedTx) ([]c3FeeExecution, error) {
	var out []c3FeeExecution
	for i, t := range txs {
		if t.Code != 0 || len(t.Data) == 0 {
			continue
		}
		x := c3FeeExecution{Index: i}
		var e error
		x.Result, x.Effects, e = protocol.DecodePublicExecution(network, t.Bytes, t.Data)
		if e != nil {
			return nil, e
		}
		if protocol.IsCompensationDecision(t.Bytes) {
			d, e := protocol.DecodeCompensationDecision(t.Bytes)
			if e != nil {
				return nil, e
			}
			x.Kind = "compensation"
			x.Decision = &d
		} else if protocol.IsRepairInput(t.Bytes) || protocol.IsRepairBatch(t.Bytes) {
			x.Kind = "history-repair"
			r, e := protocol.DecodeRepairResult(t.Data)
			if e != nil {
				return nil, e
			}
			x.HistoryApplied = &r.Applied
		} else if protocol.IsClockTick(t.Bytes, network) {
			x.Kind = "clock"
		} else {
			p, e := protocol.DecodeDirectSubmission(t.Bytes)
			if e != nil {
				return nil, e
			}
			x.Kind = "payment"
			x.Tx = p.Tx.ID()
			x.Fact = p.Summary().Fact()
		}
		out = append(out, x)
	}
	return out, nil
}

type c3FeeReport struct {
	EpochSettlements                                         []c3FeeSample
	FUELConserved                                            bool
	Version                                                  int
	Case                                                     string
	Count                                                    int
	SourceDelayNS, TimeoutSeconds, StartedUnixNS, DurationNS int64
	Pairs                                                    []*c3FeePair
	Samples                                                  []c3FeeSample
	Blocks                                                   []c3FeeBlock
	InitialPools, FinalPools                                 []committee.C3PoolSnapshot
	Error                                                    string `json:",omitempty"`
	FinalSnapshotError                                       string `json:",omitempty"`
}
type c3FeesRequests struct{ Source, Child protocol.DirectRequest }
type c3FeesFixture struct {
	Count          int
	TimeoutSeconds int64
	Requests       []c3FeesRequests
}

func c3CheckRecord(r rules.C3Record) error {
	if e := r.Escrow.Validate(); e != nil {
		return e
	}
	s := r.Escrow
	if s.Commitment.Budget != s.Held+s.Rewards+s.Burned+s.Reflow+s.Released {
		return fmt.Errorf("C3 budget conservation failed")
	}
	return nil
}

func c3FeesRequest(n cfg.Network, p rules.DirectPolicy, org protocol.OrgConfig, key ed25519.PrivateKey, in protocol.Input, coin protocol.Output, to protocol.ReceiveDescriptor, proofs []protocol.InputCertificate, fee state.OriginOutput) (protocol.DirectRequest, error) {
	if finalC3Network(n) {
		return budgetRequest(n, p, org, key, in, coin, to, proofs)
	}
	// Reuse the production builder, but bind the requested quote before creating
	// the final transaction identity and signatures.
	r, e := budgetRequest(n, p, org, key, in, coin, to, proofs, fee)
	if e != nil {
		return r, e
	}
	b := r.Tx.Body
	b.Fee.Maximum = 10
	if e = bindC3(&b, n, p); e != nil {
		return r, e
	}
	r.Tx, e = protocol.NewFastTx(b, []protocol.InputClaim{{Output: coin}}, p.Key)
	if e != nil {
		return r, e
	}
	r.Tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(r.Tx.ID(), key)}
	return r, nil
}

func c3FeesPrepare(dir string, lab cfg.Lab, n cfg.Network, count int, timeout time.Duration) error {
	marker := filepath.Join(dir, "c3-fees-fixture.json")
	if _, e := os.Stat(marker); !errors.Is(e, os.ErrNotExist) {
		return fmt.Errorf("fixture must be fresh: %s", marker)
	}
	if n.Direct == nil || n.Direct.C3 == nil || len(n.Organizations) != 2 {
		return fmt.Errorf("requires init-lab --c3 with two organizations")
	}
	var ds [2]protocol.ReceiveDescriptor
	var keys [2]ed25519.PrivateKey
	for i := range ds {
		k, e := cfg.PrivateKey(lab.Owners[i])
		if e != nil {
			return e
		}
		ds[i] = protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: n.Organizations[i].Org}, k)
		keys[i] = k
	}
	// Exact genesis outputs are shared by all variants cloned from this fixture.
	n.Genesis.Outputs = nil
	for i := 0; i < count; i++ {
		for user, d := range ds {
			assets := []protocol.Asset{protocol.AssetFUEL}
			if finalC3Network(n) {
				assets = nil
			}
			if user == 0 {
				assets = append(assets, protocol.AssetCAL)
			}
			for _, asset := range assets {
				id := protocol.OutputID(protocol.Digest("C3_FEES_ORIGIN", n.Genesis.Network[:], []byte(fmt.Sprintf("%d/%d/%d", i, user, asset))))
				amount := uint64(10000)
				if asset == protocol.AssetCAL {
					amount = 100
				}
				n.Genesis.Outputs = append(n.Genesis.Outputs, state.OriginOutput{ID: id, Fact: protocol.Digest("C3_FEES_FINAL", id[:]), Output: protocol.Output{Asset: asset, Amount: amount, Recipient: d}})
			}
		}
	}
	grants := n.Genesis.Grants[:0]
	for _, g := range n.Genesis.Grants {
		if c3Pool(n, g.Key.Account) || (g.Key.Kind != protocol.ResourceFUEL && g.Key.Kind != protocol.ResourcePolicy) {
			grants = append(grants, g)
		}
	}
	n.Genesis.Grants = grants
	accounts := n.Accounts[:0]
	for _, a := range n.Accounts {
		if a.Asset != protocol.AssetFUEL || c3Pool(n, a.Owner) {
			accounts = append(accounts, a)
		}
	}
	n.Accounts = accounts
	n.Direct.TimeoutSeconds = int64(timeout / time.Second)
	policy, e := n.Direct.Policy(n.Schedule, n.Organizations)
	if e != nil {
		return e
	}
	fixture := c3FeesFixture{Count: count, TimeoutSeconds: n.Direct.TimeoutSeconds, Requests: make([]c3FeesRequests, count)}
	var origins []state.OriginOutput
	var fees [2][]state.OriginOutput
	for _, o := range n.Genesis.Outputs {
		if o.Output.Asset == protocol.AssetCAL {
			origins = append(origins, o)
		} else {
			for i := range ds {
				if o.Output.Recipient.Owner == ds[i].Owner {
					fees[i] = append(fees[i], o)
				}
			}
		}
	}
	for i := range fixture.Requests {
		q := &fixture.Requests[i]
		origin := origins[i]
		var sourceFee, childFee state.OriginOutput
		if !finalC3Network(n) {
			sourceFee, childFee = fees[0][i], fees[1][i]
		}
		q.Source, e = c3FeesRequest(n, policy, n.Organizations[0], keys[0], protocol.Input{Kind: protocol.FinalInput, Output: origin.ID, Evidence: origin.Fact}, origin.Output, ds[1], nil, sourceFee)
		if e != nil {
			return e
		}
		vector, e := rules.PrepareDirectVector(q.Source.Tx, policy)
		if e != nil {
			return e
		}
		summary := protocol.SummaryFor(q.Source.Tx, vector)
		q.Child, e = c3FeesRequest(n, policy, n.Organizations[1], keys[1], protocol.Input{Kind: protocol.CertificateInput, Output: summary.OutputID(0), Evidence: protocol.Hash(summary.Fact())}, q.Source.Tx.Body.Outputs[0], ds[0], nil, childFee)
		if e != nil {
			return e
		}
	}
	if e := cfg.Write(lab.Network, n); e != nil {
		return e
	}
	return cfg.Write(marker, fixture)
}

func c3Get(ctx context.Context, client *http.Client, url string, dst any) (bool, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if e != nil {
		return false, e
	}
	resp, e := client.Do(req)
	if e != nil {
		return false, e
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return true, json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(dst)
}

type c3FeeClient struct {
	n          cfg.Network
	lab        cfg.Lab
	policy     rules.DirectPolicy
	client     *http.Client
	public     *transport.CommitteeClient
	keys       [2]ed25519.PrivateKey
	ds         [2]protocol.ReceiveDescriptor
	wallets    [2]*wallet.Wallet
	dbs        [2]*store.Group
	start      time.Time
	findReplay func([]byte, int64) *c3ReplayInclusion
}

func (c *c3FeeClient) pools(ctx context.Context) ([]committee.C3PoolSnapshot, error) {
	out := make([]committee.C3PoolSnapshot, len(c.n.Direct.C3.Issuers))
	for i, issuer := range c.n.Direct.C3.Issuers {
		k := issuer.Funding.Key
		ok, e := c3Get(ctx, c.client, fmt.Sprintf("%s/v3/c3/pools/%s/%d", c.n.CommitteeURLs[0], k.Account.String(), k.Version), &out[i])
		if e != nil {
			return nil, e
		}
		if !ok {
			return nil, state.ErrNotFound
		}
		s := out[i]
		if s.Funding != s.Free+s.Held+s.Spent {
			return nil, fmt.Errorf("single-grant pool does not conserve funding")
		}
	}
	return out, nil
}
func (c *c3FeeClient) escrow(ctx context.Context, f protocol.SpendFactID) (*committee.C3Snapshot, error) {
	if f == (protocol.SpendFactID{}) {
		return nil, nil
	}
	var s committee.C3Snapshot
	ok, e := c3Get(ctx, c.client, c.n.CommitteeURLs[0]+"/v3/c3/escrows/"+protocol.Hash(f).String(), &s)
	if e != nil || !ok {
		return nil, e
	}
	return &s, c3CheckRecord(s.Record)
}
func (c *c3FeeClient) obligation(ctx context.Context, id protocol.OutputID) (*rules.DirectObligation, error) {
	if id == (protocol.OutputID{}) {
		return nil, nil
	}
	var o rules.DirectObligation
	ok, e := c3Get(ctx, c.client, c.n.CommitteeURLs[0]+"/v3/obligations/"+protocol.Hash(id).String(), &o)
	if e != nil || !ok {
		return nil, e
	}
	return &o, nil
}
func (c *c3FeeClient) stage(ctx context.Context, p *c3FeePair, name string) error {
	s := c3FeeStage{Name: name, OffsetNS: time.Since(c.start).Nanoseconds()}
	var e error
	if s.Source, e = c.escrow(ctx, p.Source.Fact); e != nil {
		return e
	}
	if s.Child, e = c.escrow(ctx, p.Child.Fact); e != nil {
		return e
	}
	if s.Obligation, e = c.obligation(ctx, p.Source.Output); e != nil {
		return e
	}
	p.Stages = append(p.Stages, s)
	return nil
}
func (c *c3FeeClient) collect(ctx context.Context, owner int, r protocol.DirectRequest, h *c3FeePayment) (protocol.OutputCertificate, error) {
	h.Tx = r.Tx.ID()
	if r.Tx.Body.Fee.C3.Final() {
		h.ServiceFeeCAL = r.Tx.Body.Fee.C3.Quote
	}
	h.ExpectedReceiveCAL = r.Tx.Body.Outputs[0].Amount
	raw, e := c.wallets[owner].SaveDirectRequestBytes(r)
	if e != nil {
		return protocol.OutputCertificate{}, e
	}
	h.RequestRaw = raw
	start := time.Now()
	h.CertificateAttempts++
	cert, e := budgetSend(ctx, c.client, c.lab.Gateways[owner]+"/debug/budget/collect", raw)
	if e != nil {
		return cert, e
	}
	if e = c.wallets[1-owner].ReceiveDirect(r.Tx.Body.Outputs[0], cert, 0); e != nil {
		return cert, e
	}
	h.ReceiptNS = time.Since(start).Nanoseconds()
	h.CertifiedOffsetNS = time.Since(c.start).Nanoseconds()
	h.Fact = cert.QC.Fact
	h.Output = cert.Summary.OutputID(0)
	h.CertificateRaw, e = cert.MarshalBinary()
	if e != nil {
		return cert, e
	}
	h.SubmissionRaw, e = (protocol.DirectPayment{Tx: r.Tx, Certificate: cert, InputCertificates: r.InputCertificates}).Submission().MarshalBinary()
	return cert, e
}
func (c *c3FeeClient) submit(ctx context.Context, h *c3FeePayment, at time.Time) error {
	return budgetRelease(ctx, at, func(ctx context.Context) error {
		return c3SubmitRecorded(ctx, c.start, h.SubmissionRaw, c.public.Submit, &h.SubmitAttemptLog)
	}, func() {
		h.SubmitAttempts++
		if h.SubmitOffsetNS == 0 {
			h.SubmitOffsetNS = time.Since(c.start).Nanoseconds()
		}
	})
}
func (c *c3FeeClient) observed(ctx context.Context, h *c3FeePayment, owner int) error {
	for {
		var coin wallet.DirectCoin
		var found bool
		e := c.dbs[1-owner].View(func(v state.ReadView) error {
			var e error
			coin, found, e = state.Load[wallet.DirectCoin](v, wallet.DirectCoinKey(h.Output, 0))
			return e
		})
		if e != nil {
			return e
		}
		if found && (coin.Final != (protocol.Hash{}) || coin.Recovered) {
			if coin.Output.Asset != protocol.AssetCAL || coin.Output.Amount != h.ExpectedReceiveCAL {
				return fmt.Errorf("receiver principal mismatch")
			}
			h.FinalCoin = coin
			h.PublicOffsetNS = time.Since(c.start).Nanoseconds()
			return nil
		}
		if e = budgetPause(ctx, 10*time.Millisecond); e != nil {
			return e
		}
	}
}
func (c *c3FeeClient) feeAndClosed(ctx context.Context, h *c3FeePayment, owner int) error {
	id := protocol.OutputIdentity(c.n.Genesis.Network, h.Tx, protocol.FeeChangeIndex)
	for !finalC3Network(c.n) {
		var found bool
		e := c.dbs[owner].View(func(v state.ReadView) error {
			var e error
			h.FeeChange, found, e = state.Load[wallet.DirectCoin](v, wallet.DirectCoinKey(id, 0))
			return e
		})
		if e != nil {
			return e
		}
		if found && h.FeeChange.Final != (protocol.Hash{}) {
			if h.FeeChange.Output.Asset != protocol.AssetFUEL || h.FeeChange.Output.Recipient.Owner != c.ds[owner].Owner || h.FeeChange.Output.Amount > h.FeeInput.Output.Amount {
				return fmt.Errorf("invalid verified fee change")
			}
			break
		}
		if e = budgetPause(ctx, 10*time.Millisecond); e != nil {
			return e
		}
	}
	for {
		complete := true
		for i, url := range c.n.Members[c.n.Organizations[owner].Org] {
			s, e := loadProgressBatch(ctx, c.client, url, []protocol.SpendFactID{h.Fact})
			if e != nil {
				return e
			}
			h.MembersClosed[i] = s[0]
			if !s[0].Observed || (s[0].Signed && !s[0].Closed) {
				complete = false
			}
		}
		if complete {
			break
		}
		if e := budgetPause(ctx, 25*time.Millisecond); e != nil {
			return e
		}
	}
	// Observe both fee-output identities only after the local verified follower
	// has caught up to every member closure height. Height is a lower bound on
	// the consistent wallet snapshot, which can advance between these reads.
	var closedHeight int64
	for _, s := range h.MembersClosed {
		closedHeight = max(closedHeight, s.Height)
	}
	for {
		height, e := blockfollow.Height(c.dbs[owner])
		if e != nil {
			return e
		}
		if height >= closedHeight {
			h.FeeObservedHeight = height
			break
		}
		if e = budgetPause(ctx, 10*time.Millisecond); e != nil {
			return e
		}
	}
	refundID := protocol.OutputIdentity(c.n.Genesis.Network, h.Tx, protocol.FeeRefundIndex)
	if e := c.dbs[owner].View(func(v state.ReadView) error {
		var found bool
		var e error
		h.FeeChange, found, e = state.Load[wallet.DirectCoin](v, wallet.DirectCoinKey(id, 0))
		if e != nil {
			return e
		}
		if !found && !finalC3Network(c.n) {
			return state.ErrNotFound
		}
		if found && finalC3Network(c.n) {
			return fmt.Errorf("final C3 unexpectedly created a user FUEL change")
		}
		refund, found, e := state.Load[wallet.DirectCoin](v, wallet.DirectCoinKey(refundID, 0))
		if e != nil {
			return e
		}
		h.FeeRefund = nil
		if found {
			h.FeeRefund = &refund
		}
		h.RefundChecked = true
		return nil
	}); e != nil {
		return e
	}
	if h.FeeRefund != nil && (h.FeeRefund.Final == (protocol.Hash{}) || h.FeeRefund.Output.Asset != protocol.AssetFUEL || h.FeeRefund.Output.Recipient.Owner != c.ds[owner].Owner) {
		return fmt.Errorf("invalid verified fee refund")
	}
	var e error
	if finalC3Network(c.n) {
		if h.FeeRefund != nil || h.FeeInput.Output.Amount != 0 {
			return fmt.Errorf("final C3 has user FUEL effects")
		}
		h.FeeNet = 0
		return nil
	}
	h.FeeNet, e = c3ObservedFeeNet(*h)
	return e
}

func (c *c3FeeClient) pair(ctx context.Context, p *c3FeePair, mode string, delay time.Duration, requests c3FeesRequests, fees [2]state.OriginOutput) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	var releaseDone chan error
	defer func() {
		if releaseDone != nil {
			<-releaseDone
		}
		cancel()
	}()
	defer func() {
		if err != nil {
			p.Error = err.Error()
		}
	}()
	p.Source.FeeInput, p.Child.FeeInput = fees[0], fees[1]
	build := time.Now()
	r := requests.Source
	if r.Tx.VerifyAuth() != nil {
		return protocol.ErrAuth
	}
	p.Source.BuildNS = time.Since(build).Nanoseconds()
	cert, e := c.collect(ctx, 0, r, &p.Source)
	if e != nil {
		return e
	}
	certified := c.start.Add(time.Duration(p.Source.CertifiedOffsetNS))
	if mode == "delay" {
		releaseDone = make(chan error, 1)
		done := releaseDone
		go func() { done <- c.submit(ctx, &p.Source, certified.Add(delay)) }()
	}
	if mode == "source-first" {
		if e = c.submit(ctx, &p.Source, time.Now()); e != nil {
			return e
		}
		if e = c.observed(ctx, &p.Source, 0); e != nil {
			return e
		}
		if e = c.stage(ctx, p, "source-executed"); e != nil {
			return e
		}
	}
	// Retain the same certificate input even when the source has executed. Both
	// ordering cases therefore sign the same transaction body for a fixed QC.
	build = time.Now()
	child := requests.Child
	if child.Tx.VerifyAuth() != nil || len(child.Tx.Body.Inputs) != 1 || child.Tx.Body.Inputs[0].Output != p.Source.Output || child.Tx.Body.Inputs[0].Evidence != protocol.Hash(cert.QC.Fact) {
		return fmt.Errorf("live source QC does not match frozen child")
	}
	child.InputCertificates = []protocol.InputCertificate{{Certificate: cert, Index: 0}}
	p.Child.BuildNS = time.Since(build).Nanoseconds()
	if _, e = c.collect(ctx, 1, child, &p.Child); e != nil {
		return e
	}
	if e = c.submit(ctx, &p.Child, time.Now()); e != nil {
		return e
	}
	if e = c.observed(ctx, &p.Child, 1); e != nil {
		return e
	}
	if e = c.stage(ctx, p, "child-executed"); e != nil {
		return e
	}
	if mode == "compensated" {
		for {
			o, e := c.obligation(ctx, p.Source.Output)
			if e != nil {
				return e
			}
			if o != nil && o.Status == rules.DirectRepaired {
				break
			}
			if e = budgetPause(ctx, 20*time.Millisecond); e != nil {
				return e
			}
		}
		if e = c.stage(ctx, p, "compensated-before-source"); e != nil {
			return e
		}
		s := p.Stages[len(p.Stages)-1].Source
		if s == nil || s.Record.QuoteCollected || s.Record.Escrow.Held == 0 {
			return fmt.Errorf("compensated source lost repay budget or collected quote early")
		}
	}
	if mode == "child-first" || mode == "compensated" {
		if e = c.stage(ctx, p, "before-source-submit"); e != nil {
			return e
		}
		if e = c.submit(ctx, &p.Source, time.Now()); e != nil {
			return e
		}
	}
	if releaseDone != nil {
		e = <-releaseDone
		releaseDone = nil
		if e != nil {
			return e
		}
	}
	if p.Source.PublicOffsetNS == 0 {
		if e = c.observed(ctx, &p.Source, 0); e != nil {
			return e
		}
	}
	if e = c.feeAndClosed(ctx, &p.Source, 0); e != nil {
		return e
	}
	if e = c.feeAndClosed(ctx, &p.Child, 1); e != nil {
		return e
	}
	if e = c.stage(ctx, p, "closed"); e != nil {
		return e
	}
	last := p.Stages[len(p.Stages)-1]
	for _, s := range []*committee.C3Snapshot{last.Source, last.Child} {
		if s == nil || !s.Record.QuoteCollected || s.Record.Escrow.Held != 0 {
			return fmt.Errorf("incomplete final escrow")
		}
	}
	if mode == "compensated" {
		p.Repair = new(repairTrial)
		if e = waitRepair(ctx, c.client, c.n.CommitteeURLs[:], p.Source.Output, p.Repair); e != nil {
			return e
		}
		if e = c.stage(ctx, p, "history-repair-observed"); e != nil {
			return e
		}
		if !reflect.DeepEqual(last.Source.Record, p.Stages[len(p.Stages)-1].Source.Record) {
			return fmt.Errorf("history repair changed source fees")
		}
	}
	p.ReplayBefore, e = c.escrow(ctx, p.Source.Fact)
	if e != nil {
		return e
	}
	// A duplicate may be rejected by mempool duplicate detection. Either outcome
	// is retained. Idle Comet chains need not produce new blocks after rejection.
	height, e := blockfollow.Height(c.dbs[0])
	if e != nil {
		return e
	}
	p.Replay, e = c3ReplaySubmit(ctx, c.client, c.n.CommitteeURLs[0], p.Source.SubmissionRaw)
	if e != nil {
		p.ReplayError = e.Error()
		return fmt.Errorf("replay inconclusive: %w", e)
	}
	if p.Replay.Outcome == "http-rejected" {
		p.ReplayError = fmt.Sprintf("HTTP %d: %s", p.Replay.HTTPStatus, p.Replay.ResponseBody)
	} else {
		replayCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		for {
			p.Replay.Inclusion = c.findReplay(p.Source.SubmissionRaw, height)
			if p.Replay.Inclusion != nil {
				p.Replay.Outcome = "accepted-and-observed"
				break
			}
			if e = budgetPause(replayCtx, 25*time.Millisecond); e != nil {
				p.Replay.Outcome = "accepted-unobserved"
				p.Replay.Error = e.Error()
				p.ReplayError = e.Error()
				return fmt.Errorf("accepted replay lacks exact verified inclusion: %w", e)
			}
		}
	}
	p.ReplayAfter, e = c.escrow(ctx, p.Source.Fact)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(p.ReplayBefore.Record, p.ReplayAfter.Record) {
		return fmt.Errorf("closed replay changed escrow")
	}
	return c.feeAndClosed(ctx, &p.Source, 0)
}

func c3FeesCommand(args []string) (err error) {
	f := flag.NewFlagSet("c3-fees", flag.ContinueOnError)
	dir := f.String("dir", "", "fresh C3 lab")
	prepare := f.Bool("prepare", false, "prepare independent owner fee inputs before boot")
	count := f.Int("count", 1, "independent source-child pairs")
	mode := f.String("case", "source-first", "source-first, child-first, compensated, delay")
	delay := f.Duration("source-delay", 0, "source QC to controlled public submission")
	timeout := f.Duration("timeout", 2*time.Second, "genesis timeout, preparation only")
	drain := f.Duration("drain", 90*time.Second, "whole client deadline")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *dir == "" || *count < 1 || *count > 100 || *delay < 0 || *timeout < time.Second || *timeout%time.Second != 0 || *drain <= 0 || (*mode != "source-first" && *mode != "child-first" && *mode != "compensated" && *mode != "delay") {
		return protocol.ErrRule
	}
	var lab cfg.Lab
	var n cfg.Network
	if e := cfg.Read(filepath.Join(*dir, "lab.json"), &lab); e != nil {
		return e
	}
	if e := cfg.Read(lab.Network, &n); e != nil {
		return e
	}
	if *prepare {
		return c3FeesPrepare(*dir, lab, n, *count, *timeout)
	}
	var fixture c3FeesFixture
	if e := cfg.Read(filepath.Join(*dir, "c3-fees-fixture.json"), &fixture); e != nil {
		return e
	}
	if fixture.Count != *count || len(fixture.Requests) != *count || n.Direct == nil || n.Direct.C3 == nil {
		return fmt.Errorf("fixture count or C3 policy mismatch")
	}
	if os.Getenv("UTXO_EXPERIMENT_BUDGET") != "1" || os.Getenv("UTXO_EXPERIMENT_DISABLE_SOURCE_RECOVERY") != "1" {
		return fmt.Errorf("controlled submission requires UTXO_EXPERIMENT_BUDGET=1 and UTXO_EXPERIMENT_DISABLE_SOURCE_RECOVERY=1 for all services and client")
	}
	path := filepath.Join(*dir, "reports", "c3-fees.json")
	if _, e := os.Stat(path); !errors.Is(e, os.ErrNotExist) {
		return fmt.Errorf("report already exists: %s", path)
	}
	p, e := n.Direct.Policy(n.Schedule, n.Organizations)
	if e != nil {
		return e
	}
	trust, e := n.Trust()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), *drain)
	defer cancel()
	c := c3FeeClient{n: n, lab: lab, policy: p, client: transport.NewHTTPClient(5 * time.Second), public: transport.NewCommitteeClient(n.CommitteeURLs[0], n.CommitteeURLs[1:]...), start: time.Now()}
	report := c3FeeReport{Version: 1, Case: *mode, Count: *count, SourceDelayNS: int64(*delay), TimeoutSeconds: n.Direct.TimeoutSeconds, StartedUnixNS: c.start.UnixNano(), Pairs: make([]*c3FeePair, *count)}
	if finalC3Network(n) {
		report.Version = 2
	}
	defer func() {
		if len(report.FinalPools) == 0 {
			snapshotCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			pools, e := c.pools(snapshotCtx)
			stop()
			if e != nil {
				report.FinalSnapshotError = e.Error()
			} else {
				report.FinalPools = pools
			}
		}
		report.DurationNS = time.Since(c.start).Nanoseconds()
		if err != nil {
			report.Error = err.Error()
		}
		if e := cfg.Write(path, report); err == nil {
			err = e
		}
	}()
	var blocksMu sync.Mutex
	c.findReplay = func(raw []byte, after int64) *c3ReplayInclusion {
		blocksMu.Lock()
		defer blocksMu.Unlock()
		return c3FindReplay(report.Blocks, raw, after)
	}
	for i := range c.wallets {
		c.keys[i], err = cfg.PrivateKey(lab.Owners[i])
		if err != nil {
			return err
		}
		c.ds[i] = protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: n.Organizations[i].Org}, c.keys[i])
		db, e := store.OpenNoSync(filepath.Join(*dir, fmt.Sprintf("c3-wallet-%d.db", i)), store.Identity{Network: n.ChainID, Role: "c3-fees-wallet", Node: fmt.Sprint(i), Schema: 4})
		if e != nil {
			return e
		}
		g, e := store.NewGroup(db, 256, 64)
		if e != nil {
			db.Close()
			return e
		}
		c.dbs[i] = g
		defer g.Close()
		c.wallets[i], e = wallet.NewWithGenesis(g, n.Genesis.Network, c.ds[i].Owner, n.Organizations, n.Genesis, p)
		if e != nil {
			return e
		}
		prepareBlock := c.wallets[i].PrepareBlock
		if i == 0 {
			original := prepareBlock
			prepareBlock = func(b finality.VerifiedBlock) (blockfollow.Apply, error) {
				apply, e := original(b)
				if e != nil {
					return nil, e
				}
				txs := b.Transactions()
				if len(txs) > 0 {
					executions, e := c3DecodeExecutions(n.Genesis.Network, txs)
					if e != nil {
						return nil, e
					}
					blocksMu.Lock()
					report.Blocks = append(report.Blocks, c3FeeBlock{Height: b.Height(), Hash: b.Hash(), Transactions: txs, Executions: executions})
					blocksMu.Unlock()
				}
				return apply, nil
			}
		}
		defer blockfollow.Start(ctx, cancel, g, transport.NewCommitteeClient(n.CommitteeURLs[0]), trust, prepareBlock)()
	}
	if report.InitialPools, err = c.pools(ctx); err != nil {
		return err
	}
	sampleCtx, stopSamples := context.WithCancel(ctx)
	samplesDone := make(chan struct{})
	go func() {
		defer close(samplesDone)
		for {
			s := c3FeeSample{OffsetNS: time.Since(c.start).Nanoseconds()}
			s.Pools, e = c.pools(sampleCtx)
			if e != nil {
				s.Error = e.Error()
			}
			for _, org := range n.Organizations {
				for _, url := range n.Members[org.Org] {
					var snap budgetprobe.Snapshot
					ok, e := c3Get(sampleCtx, c.client, url+"/debug/budget", &snap)
					if e != nil {
						s.Error = e.Error()
					} else if !ok {
						s.Error = "member budget endpoint missing"
					} else {
						s.Members = append(s.Members, c3FeeMember{url, snap})
					}
				}
			}
			if sampleCtx.Err() != nil {
				return
			}
			report.Samples = append(report.Samples, s)
			if budgetPause(sampleCtx, 50*time.Millisecond) != nil {
				return
			}
		}
	}()
	defer func() { stopSamples(); <-samplesDone }()
	var origins []state.OriginOutput
	var fees [2][]state.OriginOutput
	for _, o := range n.Genesis.Outputs {
		if o.Output.Asset == protocol.AssetCAL && o.Output.Recipient.Owner == c.ds[0].Owner {
			origins = append(origins, o)
		}
		for i := range fees {
			if o.Output.Asset == protocol.AssetFUEL && o.Output.Recipient.Owner == c.ds[i].Owner {
				fees[i] = append(fees[i], o)
			}
		}
	}
	if finalC3Network(n) {
		if len(fees[0]) != 0 || len(fees[1]) != 0 {
			return fmt.Errorf("final C3 user genesis contains FUEL")
		}
		fees[0], fees[1] = make([]state.OriginOutput, *count), make([]state.OriginOutput, *count)
	}
	if len(origins) != *count || len(fees[0]) != *count || len(fees[1]) != *count {
		return fmt.Errorf("independent input counts mismatch")
	}
	var wg sync.WaitGroup
	for i := range report.Pairs {
		pair := &c3FeePair{Index: i}
		report.Pairs[i] = pair
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = c.pair(ctx, pair, *mode, *delay, fixture.Requests[i], [2]state.OriginOutput{fees[0][i], fees[1][i]})
		}(i)
	}
	wg.Wait()
	blocksMu.Lock()
	for _, b := range report.Blocks {
		for _, x := range b.Executions {
			if x.Kind != "payment" || !x.Result.Applied {
				continue
			}
			for _, pair := range report.Pairs {
				for _, h := range []*c3FeePayment{&pair.Source, &pair.Child} {
					if h.Tx == x.Tx {
						h.ExecutionHeight = b.Height
						h.ExecutionIndex = x.Index
						h.MissingInputs = x.Result.MissingInputs
						h.RecoveredOutputs = x.Result.RecoveredOutputs
					}
				}
			}
		}
	}
	blocksMu.Unlock()
	for _, pair := range report.Pairs {
		if pair.Error != "" {
			return fmt.Errorf("pair %d: %s", pair.Index, pair.Error)
		}
		if pair.Source.ExecutionHeight <= 0 || pair.Child.ExecutionHeight <= 0 {
			return fmt.Errorf("pair %d lacks verified execution evidence", pair.Index)
		}
		if (*mode == "source-first" && len(pair.Child.MissingInputs) != 0) || (*mode == "child-first" && (len(pair.Child.MissingInputs) != 1 || len(pair.Source.RecoveredOutputs) != 0)) || (*mode == "compensated" && len(pair.Source.RecoveredOutputs) != 1) {
			return fmt.Errorf("pair %d committed order differs from requested %s case", pair.Index, *mode)
		}
	}
	if finalC3Network(n) {
		// Exercise two real public epoch boundaries after economic closure.
		// Clock ticks pass ordinary committee validation and verified followers.
		pools, e := c.pools(ctx)
		if e != nil || len(pools) == 0 {
			return fmt.Errorf("epoch initial snapshot: %w", e)
		}
		targetEpoch := pools[0].Epoch.Epoch + 2
		for pools[0].Epoch.Epoch < targetEpoch {
			height, e := blockfollow.Height(c.dbs[0])
			if e != nil {
				return e
			}
			// Clock commands already have their own wire format; the payment
			// relay's Submit API would try to wrap them as signed payments.
			tick, e := c3ReplaySubmit(ctx, c.client, n.CommitteeURLs[0], protocol.ClockTick(n.Genesis.Network, height+1))
			if e != nil {
				return e
			}
			if tick.HTTPStatus != 202 {
				return fmt.Errorf("epoch tick HTTP %d: %s", tick.HTTPStatus, tick.ResponseBody)
			}
			for {
				observed, e := blockfollow.Height(c.dbs[0])
				if e != nil {
					return e
				}
				if observed > height {
					break
				}
				if e = budgetPause(ctx, 25*time.Millisecond); e != nil {
					return e
				}
			}
			pools, e = c.pools(ctx)
			if e != nil {
				return e
			}
			report.EpochSettlements = append(report.EpochSettlements, c3FeeSample{OffsetNS: time.Since(c.start).Nanoseconds(), Pools: pools})
		}
	}
	report.FinalPools, err = c.pools(ctx)
	if err == nil && finalC3Network(n) {
		err = c3FinalBalances(&report)
	}
	return err
}

func c3FinalBalances(r *c3FeeReport) error {
	if len(r.InitialPools) != 2 || len(r.FinalPools) != 2 {
		return fmt.Errorf("expected two issuer snapshots")
	}
	var initial, current uint64
	var e error
	for i, p := range r.FinalPools {
		initial, e = protocol.Add(initial, r.InitialPools[i].Free+r.InitialPools[i].Held)
		if e != nil {
			return e
		}
		current, e = protocol.Add(current, p.Free+p.Held)
		if e != nil {
			return e
		}
		var quotes uint64
		for _, pair := range r.Pairs {
			h := pair.Source
			if i == 1 {
				h = pair.Child
			}
			quotes, e = protocol.Add(quotes, h.ServiceFeeCAL)
			if e != nil {
				return e
			}
		}
		if p.CALRevenue < r.InitialPools[i].CALRevenue || p.CALRevenue-r.InitialPools[i].CALRevenue != quotes {
			return fmt.Errorf("issuer %d CAL revenue does not match executed signed quotes", i)
		}
		if p.Burned != 0 || p.PoolBurned != 0 {
			return fmt.Errorf("final C3 burned FUEL")
		}
	}
	initial, e = protocol.Add(initial, r.InitialPools[0].Rewards)
	if e != nil {
		return e
	}
	current, e = protocol.Add(current, r.FinalPools[0].Rewards)
	if e != nil {
		return e
	}
	for _, part := range r.InitialPools[0].Epoch.Pool {
		initial, e = protocol.Add(initial, part.Amount)
		if e != nil {
			return e
		}
	}
	for _, part := range r.FinalPools[0].Epoch.Pool {
		current, e = protocol.Add(current, part.Amount)
		if e != nil {
			return e
		}
	}
	r.FUELConserved = initial == current
	if !r.FUELConserved {
		return fmt.Errorf("FUEL total mismatch: initial=%d current=%d", initial, current)
	}
	return nil
}
