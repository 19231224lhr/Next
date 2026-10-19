package main

// This opt-in client controls publication only. Certification, ownership,
// quorum checks, public execution, and verified observation remain real.
import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/committee"
	"utxo/internal/requesttrace"
	"utxo/internal/rules"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type c3CongestionFixture struct {
	Ordinary, Sources int
	TimeoutSeconds    int64
}
type c3CongestionRow struct {
	HTTPAttempts                                     []c3CongestionHTTPAttempt
	Index, Owner                                     int
	Class                                            string
	PlannedSubmissionOrder, ActualSubmissionOrder    int
	BaseFeeCAL, PriorityFeeCAL, PriorityRewardFUEL   uint64
	PlannedSubmitOffsetNS, VerifiedBlockSeenOffsetNS int64
	Payment                                          c3FeePayment
	FinalEscrow                                      *committee.C3Snapshot
	InitialObligation                                *rules.DirectObligation `json:",omitempty"`
	Error                                            string                  `json:",omitempty"`
}
type c3CongestionHTTPAttempt struct {
	URL                        string
	StartOffsetNS, EndOffsetNS int64
	HTTPStatus                 int
	ResponseBody, Error        string `json:",omitempty"`
}

func c3CongestionHTTP(ctx context.Context, client *http.Client, url string, raw []byte, start time.Time) (a c3CongestionHTTPAttempt, err error) {
	a.URL = url + "/v1/commands"
	defer func() {
		if err != nil {
			a.Error = err.Error()
		}
	}()
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, a.URL, bytes.NewReader(raw))
	if e != nil {
		return a, e
	}
	req.Header.Set("Content-Type", transport.MediaType)
	req.Header.Set(requesttrace.DeliverySourceHeader, "c3-congestion")
	req.Header.Set(requesttrace.DeliveryTimeHeader, strconv.FormatInt(time.Now().UnixNano(), 10))
	a.StartOffsetNS = time.Since(start).Nanoseconds()
	resp, e := client.Do(req)
	if e != nil {
		a.EndOffsetNS = time.Since(start).Nanoseconds()
		return a, e
	}
	defer resp.Body.Close()
	a.HTTPStatus = resp.StatusCode
	body, e := io.ReadAll(io.LimitReader(resp.Body, 4096))
	a.EndOffsetNS = time.Since(start).Nanoseconds()
	a.ResponseBody = string(body)
	if e != nil {
		return a, e
	}
	if resp.StatusCode != http.StatusAccepted {
		return a, fmt.Errorf("HTTP %d: %s", resp.StatusCode, a.ResponseBody)
	}
	return a, nil
}

type c3CongestionBlock struct {
	SeenOffsetNS int64
	Block        c3FeeBlock
}
type c3CongestionReport struct {
	ConsensusBlockBytes                                          int64
	Version                                                      int
	StartedUnixNS, DurationNS, WaveStartOffsetNS, TimeoutSeconds int64
	OrdinaryCount, SourceCount, SourceAfter                      int
	SubmissionIntervalNS                                         int64
	ReservedBPS                                                  uint32
	EpochBlocks                                                  uint64
	Rows                                                         []*c3CongestionRow
	Blocks                                                       []c3CongestionBlock
	InitialPools, FinalPools                                     []committee.C3PoolSnapshot
	FUELConserved                                                bool
	Error, FinalSnapshotError                                    string `json:",omitempty"`
}

func c3CongestionPriority(r protocol.DirectRequest, n cfg.Network, p rules.DirectPolicy, key ed25519.PrivateKey) (protocol.DirectRequest, error) {
	if !finalC3Network(n) || len(r.Tx.Claims) != 1 || len(r.Tx.Body.Outputs) != 1 {
		return r, protocol.ErrRule
	}
	var tier protocol.Hash
	for _, policy := range n.Direct.C3.Policies {
		if policy.ID == n.Direct.C3.Current {
			for _, candidate := range policy.Tiers {
				if candidate.CAL == 10 && candidate.MinimumReward == 2 {
					tier = candidate.ID
				}
			}
		}
	}
	if tier == (protocol.Hash{}) {
		return r, fmt.Errorf("requires published CAL10 / FUEL2 priority tier")
	}
	b := r.Tx.Body
	b.Outputs = append([]protocol.Output(nil), b.Outputs...)
	b.Fee.C3.Tier = tier
	if e := bindC3(&b, n, p); e != nil {
		return r, e
	}
	if r.Tx.Claims[0].Output.Amount <= b.Fee.Maximum {
		return r, fmt.Errorf("priority quote consumes principal")
	}
	b.Outputs[0].Amount = r.Tx.Claims[0].Output.Amount - b.Fee.Maximum
	tx, e := protocol.NewFastTx(b, r.Tx.Claims, p.Key)
	if e != nil {
		return r, e
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), key)}
	r.Tx = tx
	return r, nil
}

func c3CongestionCommand(args []string) (err error) {
	f := flag.NewFlagSet("c3-congestion", flag.ContinueOnError)
	dir := f.String("dir", "", "fresh final-C3 lab")
	prepare := f.Bool("prepare", false, "prepare genesis and independent requests before services start")
	count := f.Int("count", 60, "ordinary independent payments (recommended 40–80)")
	sources := f.Int("sources", 4, "pre-opened source obligations to close during the wave")
	sourceAfter := f.Int("source-after", -1, "insert recovery sources after this many ordinary submissions; -1 means halfway")
	interval := f.Duration("submit-interval", 0, "planned interval between publication calls; zero is a burst")
	timeout := f.Duration("timeout", 5*time.Minute, "genesis obligation timeout; preparation only")
	drain := f.Duration("drain", 2*time.Minute, "whole client deadline")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *dir == "" || *count < 1 || *count > 90 || *sources < 1 || *sources > 10 || *count+*sources > 100 || *interval < 0 || *drain <= 0 || *timeout < time.Minute || *timeout%time.Second != 0 {
		return protocol.ErrRule
	}
	if *sourceAfter < 0 {
		*sourceAfter = *count / 2
	}
	if *sourceAfter > *count {
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
	if !finalC3Network(n) {
		return fmt.Errorf("requires init-lab --c3-final")
	}
	manifest := filepath.Join(*dir, "c3-congestion-fixture.json")
	if *prepare {
		if e := c3FeesPrepare(*dir, lab, n, *count+*sources, *timeout); e != nil {
			return e
		}
		return cfg.Write(manifest, c3CongestionFixture{*count, *sources, int64(*timeout / time.Second)})
	}
	var setup c3CongestionFixture
	var fixture c3FeesFixture
	if e := cfg.Read(manifest, &setup); e != nil {
		return e
	}
	if e := cfg.Read(filepath.Join(*dir, "c3-fees-fixture.json"), &fixture); e != nil {
		return e
	}
	if setup.Ordinary != *count || setup.Sources != *sources || len(fixture.Requests) != *count+*sources {
		return fmt.Errorf("prepared congestion count mismatch")
	}
	if time.Duration(n.Direct.TimeoutSeconds)*time.Second < *drain+30*time.Second {
		return fmt.Errorf("prepared timeout must exceed drain by at least 30s")
	}
	if os.Getenv("UTXO_EXPERIMENT_BUDGET") != "1" || os.Getenv("UTXO_EXPERIMENT_DISABLE_SOURCE_RECOVERY") != "1" {
		return fmt.Errorf("all services and client require UTXO_EXPERIMENT_BUDGET=1 and UTXO_EXPERIMENT_DISABLE_SOURCE_RECOVERY=1")
	}
	path := filepath.Join(*dir, "reports", "c3-congestion.json")
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
	c := c3FeeClient{n: n, lab: lab, policy: p, client: transport.NewHTTPClient(5 * time.Second), public: transport.NewCommitteeClient(n.CommitteeURLs[0]), start: time.Now()}
	report := c3CongestionReport{Version: 2, ConsensusBlockBytes: n.ConsensusBlockBytes, StartedUnixNS: c.start.UnixNano(), TimeoutSeconds: n.Direct.TimeoutSeconds, OrdinaryCount: *count, SourceCount: *sources, SourceAfter: *sourceAfter, SubmissionIntervalNS: int64(*interval), ReservedBPS: n.Direct.C3.ReservedBPS, EpochBlocks: n.Direct.C3.EpochBlocks}
	var blocksMu sync.Mutex
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
		if e := c3CongestionCSV(filepath.Join(*dir, "reports", "c3-congestion.csv"), report.Rows); err == nil {
			err = e
		}
	}()
	for i := range c.wallets {
		c.keys[i], err = cfg.PrivateKey(lab.Owners[i])
		if err != nil {
			return err
		}
		c.ds[i] = protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: n.Organizations[i].Org}, c.keys[i])
		db, e := store.OpenNoSync(filepath.Join(*dir, fmt.Sprintf("c3-congestion-wallet-%d.db", i)), store.Identity{Network: n.ChainID, Role: "c3-congestion-wallet", Node: fmt.Sprint(i), Schema: 4})
		if e != nil {
			return e
		}
		group, e := store.NewGroup(db, 256, 64)
		if e != nil {
			db.Close()
			return e
		}
		c.dbs[i] = group
		defer group.Close()
		c.wallets[i], e = wallet.NewWithGenesis(group, n.Genesis.Network, c.ds[i].Owner, n.Organizations, n.Genesis, p)
		if e != nil {
			return e
		}
		original := c.wallets[i].PrepareBlock
		prepareBlock := original
		if i == 0 {
			prepareBlock = func(b finality.VerifiedBlock) (blockfollow.Apply, error) {
				apply, e := original(b)
				if e != nil {
					return nil, e
				}
				txs := b.Transactions()
				executions, e := c3DecodeExecutions(n.Genesis.Network, txs)
				if e != nil {
					return nil, e
				}
				blocksMu.Lock()
				report.Blocks = append(report.Blocks, c3CongestionBlock{time.Since(c.start).Nanoseconds(), c3FeeBlock{Height: b.Height(), Hash: b.Hash(), Transactions: txs, Executions: executions}})
				blocksMu.Unlock()
				return apply, nil
			}
		}
		defer blockfollow.Start(ctx, cancel, group, transport.NewCommitteeClient(n.CommitteeURLs[0]), trust, prepareBlock)()
	}
	if report.InitialPools, err = c.pools(ctx); err != nil {
		return err
	}
	add := func(class string, owner int) *c3CongestionRow {
		row := &c3CongestionRow{Index: len(report.Rows), Owner: owner, Class: class, PlannedSubmissionOrder: -1, ActualSubmissionOrder: -1}
		report.Rows = append(report.Rows, row)
		return row
	}
	collect := func(row *c3CongestionRow, r protocol.DirectRequest) (protocol.OutputCertificate, error) {
		row.BaseFeeCAL = r.Tx.Body.Fee.C3.BaseFee
		row.PriorityFeeCAL = r.Tx.Body.Fee.C3.PriorityFee
		row.PriorityRewardFUEL = r.Tx.Body.Fee.C3.PriorityReward
		cert, e := c.collect(ctx, row.Owner, r, &row.Payment)
		if e != nil {
			row.Error = e.Error()
		}
		return cert, e
	}
	submit := func(row *c3CongestionRow) error {
		return budgetRelease(ctx, time.Now(), func(ctx context.Context) error {
			a, e := c3CongestionHTTP(ctx, c.client, n.CommitteeURLs[0], row.Payment.SubmissionRaw, c.start)
			row.HTTPAttempts = append(row.HTTPAttempts, a)
			row.Payment.SubmitAttemptLog = append(row.Payment.SubmitAttemptLog, c3SubmitAttempt{StartOffsetNS: a.StartOffsetNS, EndOffsetNS: a.EndOffsetNS, Accepted: e == nil, Error: a.Error})
			if row.Payment.SubmitOffsetNS == 0 {
				row.Payment.SubmitOffsetNS = a.StartOffsetNS
			}
			return e
		}, func() { row.Payment.SubmitAttempts++ })
	}
	var recovery []*c3CongestionRow
	// Sources remain unpublished. Their children genuinely execute first and
	// create public DirectOpen obligations before the congestion wave begins.
	for i := 0; i < *sources; i++ {
		requests := fixture.Requests[*count+i]
		source := add("source-recovery", 0)
		cert, e := collect(source, requests.Source)
		if e != nil {
			return e
		}
		requests.Child.InputCertificates = []protocol.InputCertificate{{Certificate: cert, Index: 0}}
		child := add("setup-child", 1)
		if _, e = collect(child, requests.Child); e != nil {
			return e
		}
		if e = submit(child); e != nil {
			return e
		}
		if e = c.observed(ctx, &child.Payment, 1); e != nil {
			return e
		}
		ob, e := c.obligation(ctx, source.Payment.Output)
		if e != nil {
			return e
		}
		if ob == nil || ob.Status != rules.DirectOpen {
			return fmt.Errorf("source %d lacks an executed child obligation", i)
		}
		source.InitialObligation = ob
		recovery = append(recovery, source)
	}
	var ordinary []*c3CongestionRow
	for i := 0; i < *count; i++ {
		r := fixture.Requests[i].Source
		class := "ordinary-base"
		build := time.Now()
		if i%2 == 1 {
			r, e = c3CongestionPriority(r, n, p, c.keys[0])
			if e != nil {
				return e
			}
			class = "ordinary-priority"
		}
		row := add(class, 0)
		row.Payment.BuildNS = time.Since(build).Nanoseconds()
		if _, e = collect(row, r); e != nil {
			return e
		}
		ordinary = append(ordinary, row)
	}
	var wave []*c3CongestionRow
	for i := 0; i <= len(ordinary); i++ {
		if i == *sourceAfter {
			wave = append(wave, recovery...)
		}
		if i < len(ordinary) {
			wave = append(wave, ordinary[i])
		}
	}
	report.WaveStartOffsetNS = time.Since(c.start).Nanoseconds()
	waveStart := time.Now()
	for i, row := range wave {
		row.PlannedSubmissionOrder = i
		row.PlannedSubmitOffsetNS = report.WaveStartOffsetNS + int64(time.Duration(i)**interval)
		if e = budgetPauseUntil(ctx, waveStart.Add(time.Duration(i)**interval)); e != nil {
			return e
		}
		row.ActualSubmissionOrder = i
		if e = submit(row); e != nil {
			row.Error = e.Error()
			return e
		}
	}
	for _, row := range report.Rows {
		if e = c.observed(ctx, &row.Payment, row.Owner); e != nil {
			row.Error = e.Error()
			return e
		}
		if e = c.feeAndClosed(ctx, &row.Payment, row.Owner); e != nil {
			row.Error = e.Error()
			return e
		}
		row.FinalEscrow, e = c.escrow(ctx, row.Payment.Fact)
		if e != nil {
			return e
		}
		if row.FinalEscrow == nil || !row.FinalEscrow.Record.QuoteCollected || row.FinalEscrow.Record.Escrow.Held != 0 || row.FinalEscrow.Record.Escrow.Reflow != 0 {
			return fmt.Errorf("row %d did not close normally", row.Index)
		}
	}
	blocksMu.Lock()
	for _, block := range report.Blocks {
		for _, x := range block.Block.Executions {
			if x.Kind != "payment" || !x.Result.Applied {
				continue
			}
			for _, row := range report.Rows {
				if row.Payment.Tx == x.Tx {
					row.Payment.ExecutionHeight = block.Block.Height
					row.Payment.ExecutionIndex = x.Index
					row.Payment.MissingInputs = x.Result.MissingInputs
					row.Payment.RecoveredOutputs = x.Result.RecoveredOutputs
					row.VerifiedBlockSeenOffsetNS = block.SeenOffsetNS
				}
			}
		}
	}
	blocksMu.Unlock()
	for _, row := range report.Rows {
		if row.Payment.ExecutionHeight <= 0 {
			return fmt.Errorf("row %d lacks verified inclusion", row.Index)
		}
	}
	if report.FinalPools, e = c.pools(ctx); e != nil {
		return e
	}
	// Reuse the existing exact two-issuer CAL/FUEL reconciliation, placing each
	// executed payment under its actual issuer. No absent fixture is charged.
	fees := c3FeeReport{InitialPools: report.InitialPools, FinalPools: report.FinalPools}
	for _, row := range report.Rows {
		pair := &c3FeePair{}
		if row.Owner == 0 {
			pair.Source = row.Payment
		} else {
			pair.Child = row.Payment
		}
		fees.Pairs = append(fees.Pairs, pair)
	}
	if e = c3FinalBalances(&fees); e != nil {
		return e
	}
	report.FUELConserved = fees.FUELConserved
	return nil
}

func budgetPauseUntil(ctx context.Context, at time.Time) error {
	if wait := time.Until(at); wait > 0 {
		return budgetPause(ctx, wait)
	}
	return nil
}

func c3CongestionCSV(path string, rows []*c3CongestionRow) error {
	file, e := os.Create(path)
	if e != nil {
		return e
	}
	defer file.Close()
	w := csv.NewWriter(file)
	defer w.Flush()
	if e = w.Write([]string{"index", "class", "issuer", "tx", "base_fee_cal", "priority_fee_cal", "priority_reward_fuel", "receipt_ns", "planned_order", "actual_order", "planned_submit_offset_ns", "http_start_offset_ns", "http_end_offset_ns", "execution_height", "execution_index", "verified_seen_offset_ns", "closed", "error"}); e != nil {
		return e
	}
	integer := func(n int64) string { return strconv.FormatInt(n, 10) }
	for _, r := range rows {
		start, end := int64(0), int64(0)
		if len(r.Payment.SubmitAttemptLog) > 0 {
			start = r.Payment.SubmitAttemptLog[0].StartOffsetNS
			end = r.Payment.SubmitAttemptLog[len(r.Payment.SubmitAttemptLog)-1].EndOffsetNS
		}
		closed := r.FinalEscrow != nil && r.FinalEscrow.Record.Escrow.Held == 0
		if e = w.Write([]string{strconv.Itoa(r.Index), r.Class, strconv.Itoa(r.Owner), protocol.Hash(r.Payment.Tx).String(), strconv.FormatUint(r.BaseFeeCAL, 10), strconv.FormatUint(r.PriorityFeeCAL, 10), strconv.FormatUint(r.PriorityRewardFUEL, 10), integer(r.Payment.ReceiptNS), strconv.Itoa(r.PlannedSubmissionOrder), strconv.Itoa(r.ActualSubmissionOrder), integer(r.PlannedSubmitOffsetNS), integer(start), integer(end), integer(r.Payment.ExecutionHeight), strconv.Itoa(r.Payment.ExecutionIndex), integer(r.VerifiedBlockSeenOffsetNS), strconv.FormatBool(closed), r.Error}); e != nil {
			return e
		}
	}
	w.Flush()
	return w.Error()
}
