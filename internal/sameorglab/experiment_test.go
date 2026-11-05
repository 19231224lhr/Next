package sameorglab_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
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

const chain = "same-org-experiment"

type variant struct {
	continuation       bool
	compactReply       bool
	name               string
	cache, refs, order bool
}

var variants = []variant{{name: "baseline"}, {name: "A-cache", cache: true}, {name: "B-references", refs: true}, {name: "C-source-first", order: true}, {name: "ABC", cache: true, refs: true, order: true}}

type lab struct {
	f                       testkit.Fixture
	settings                rules.DirectSettings
	policy                  rules.DirectPolicy
	orgs                    []protocol.OrgConfig
	keys                    [2]ed25519.PrivateKey
	desc                    [2]protocol.ReceiveDescriptor
	members                 [4]*member.Member
	dbs                     [4]store.Store
	clients                 [4]*transport.MemberClient
	wallets                 [2]*wallet.Wallet
	collector               *gateway.Collector
	bytes, requests, misses atomic.Int64
	responseBytes           atomic.Int64
	variant                 variant
}

func newLab(t *testing.T, v variant) *lab {
	return newLabWithStore(t, v, nil)
}

func newLabWithStore(t *testing.T, v variant, factory func(string, string, store.Identity) (store.Store, error)) *lab {
	return newLabConfigured(t, v, factory, nil)
}

func newLabConfigured(t *testing.T, v variant, factory func(string, string, store.Identity) (store.Store, error), configure func(*lab)) *lab {
	t.Helper()
	l := &lab{f: testkit.NewFixture(chain, "org", 1), variant: v}
	l.f.EnableDirect()
	l.keys[0] = l.f.Owner
	seed := protocol.Digest("same-org-bob")
	l.keys[1] = ed25519.NewKeyFromSeed(seed[:])
	for i := range l.keys {
		l.desc[i] = protocol.NewDescriptor(l.f.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: l.f.Org.Org}, l.keys[i])
		l.f.Genesis.Wallets = append(l.f.Genesis.Wallets, state.WalletBinding{Owner: l.desc[i].Owner, Organization: l.f.Org.Org})
	}
	for _, g := range l.f.Genesis.Grants {
		if g.Key.Kind == protocol.ResourcePolicy {
			g.ID = protocol.Digest("bob-policy-grant")
			g.Key.Account = protocol.Digest("bob-policy")
			g.Subject = l.desc[1].Owner
			l.f.Genesis.Grants = append(l.f.Genesis.Grants, g)
			break
		}
	}
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	must(t, err)
	block, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	must(t, err)
	l.settings = rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	if configure != nil {
		configure(l)
	}
	if len(l.orgs) == 0 {
		l.orgs = []protocol.OrgConfig{l.f.Org}
	}
	l.policy, err = l.settings.Policy(l.f.Schedule, l.orgs)
	must(t, err)
	open := func(name string) store.Store {
		path := filepath.Join(t.TempDir(), name+".db")
		id := store.Identity{Network: chain, Role: name, Node: name, Schema: member.DirectStoreSchema}
		var db store.Store
		var err error
		if factory == nil {
			db, err = store.Open(path, id)
		} else {
			db, err = factory(name, path, id)
		}
		must(t, err)
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	var peers [4]gateway.MemberClient
	for i := range l.members {
		l.dbs[i] = open(fmt.Sprintf("member%d", i))
		cfg := member.Config{Organization: l.f.Org, Index: uint16(i), Key: l.f.Keys[i], Peers: l.orgs, Schedule: l.f.Schedule, Workers: 1, Direct: &l.settings, ReuseDirectVerification: v.cache}
		if v.continuation {
			cfg.DirectContinuationEntries = 256
		}
		if v.cache || v.refs {
			cfg.DirectCacheEntries = 256
		}
		l.members[i], err = member.New(cfg, l.dbs[i], l.f.Genesis)
		must(t, err)
		handler := transport.MemberHandler(l.members[i], 16, 16)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/v3/transactions") {
				l.bytes.Add(r.ContentLength)
				l.requests.Add(1)
				sw := &statusWriter{ResponseWriter: w}
				handler.ServeHTTP(sw, r)
				l.responseBytes.Add(sw.written)
				if sw.status == 428 {
					l.misses.Add(1)
				}
				return
			}
			handler.ServeHTTP(w, r)
		}))
		t.Cleanup(srv.Close)
		l.clients[i] = transport.NewMemberClient(srv.URL)
		l.clients[i].PreferDirectReferences = v.refs
		peers[i] = l.clients[i]
	}
	l.collector, err = gateway.New(l.f.Org, peers, open("gateway"))
	must(t, err)
	l.collector.SameOrgContinuation = v.continuation
	l.collector.CompactDirectApprovals = v.compactReply
	for i := range l.wallets {
		l.wallets[i], err = wallet.NewWithGenesis(open(fmt.Sprintf("wallet%d", i)), l.f.Org.Network, l.desc[i].Owner, l.orgs, l.f.Genesis)
		must(t, err)
	}
	return l
}

type statusWriter struct {
	http.ResponseWriter
	status  int
	written int64
}

func (w *statusWriter) Write(b []byte) (int, error) {
	n, e := w.ResponseWriter.Write(b)
	w.written += int64(n)
	return n, e
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		t.Fatalf("%s:%d: %v", filepath.Base(file), line, err)
	}
}

func (l *lab) request(t *testing.T, index int, previous *protocol.DirectPayment) protocol.DirectRequest {
	t.Helper()
	baseFixture := l.f
	baseFixture.Genesis.Grants = nil
	for _, g := range l.f.Genesis.Grants {
		if g.Organization != l.f.Org.Hash() {
			continue
		}
		if g.Key.Kind != protocol.ResourcePolicy || g.Subject == l.desc[0].Owner {
			baseFixture.Genesis.Grants = append(baseFixture.Genesis.Grants, g)
		}
	}
	base, err := baseFixture.FastTransaction(0, uint64(index+1), l.policy)
	must(t, err)
	body := base.Body
	sender := index % 2
	body.Subject = l.desc[sender].Owner
	body.Outputs = []protocol.Output{{Asset: protocol.AssetCAL, Amount: 100, Recipient: l.desc[1-sender]}}
	body.Admission = nil
	for _, g := range l.f.Genesis.Grants {
		if g.Organization != l.f.Org.Hash() {
			continue
		}
		if g.Key.Kind == protocol.ResourcePolicy {
			if g.Subject != body.Subject {
				continue
			}
			body.Fee.Policy = g.Key.Account
		}
		body.Admission = append(body.Admission, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
	}
	claim := l.f.Genesis.Outputs[0].Output
	var parents []protocol.InputCertificate
	if previous != nil {
		body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: previous.Certificate.Summary.OutputID(0), Evidence: protocol.Hash(previous.Certificate.QC.Fact)}}
		claim = previous.Tx.Body.Outputs[0]
		parents = []protocol.InputCertificate{{Certificate: previous.Certificate, Index: 0}}
	}
	tx, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: claim}}, l.policy.Key)
	must(t, err)
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), l.keys[sender])}
	return protocol.DirectRequest{Tx: tx, InputCertificates: parents}
}
func (l *lab) install(t *testing.T, p protocol.DirectPayment) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for _, c := range l.clients {
		wg.Add(1)
		go func(c *transport.MemberClient) { defer wg.Done(); errs <- c.InstallDirect(context.Background(), p) }(c)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		must(t, err)
	}
}
func (l *lab) quota(t *testing.T, kind protocol.ResourceKind) uint64 {
	t.Helper()
	var peak uint64
	for _, g := range l.f.Genesis.Grants {
		if g.Key.Kind != kind {
			continue
		}
		for _, m := range l.members {
			q, err := m.Quota(g.Key, 0)
			must(t, err)
			if q.Reserved > peak {
				peak = q.Reserved
			}
		}
	}
	return peak
}

type result struct {
	WorkloadHash                                                                   string
	Variant, Scenario                                                              string
	Round, Payments                                                                int
	ReadyMS                                                                        []float64
	ReadyP50MS, ReadyP95MS, ReadyMeanMS, ReadyTotalMS, InstallMS, ForegroundWallMS float64
	RequestBytes, Requests, ReferenceMisses                                        int64
	CacheHits, CacheMisses                                                         uint64
	CALPeak, CALFinal, FUELFinal, GapPeak, GapFinal                                uint64
	MissingSources                                                                 int
	CALSteps, FUELSteps, GapSteps                                                  []uint64
	PublicProcessingMS                                                             float64
}

func run(t *testing.T, v variant, scenario string, n int, plan []protocol.FastTx) result {
	t.Helper()
	l := newLab(t, v)
	r := result{Variant: v.name, Scenario: scenario, Payments: n}
	payments := make([]protocol.DirectPayment, 0, n)
	started := time.Now()
	for i := 0; i < n; i++ {
		var previous *protocol.DirectPayment
		if i > 0 {
			previous = &payments[i-1]
		}
		var req protocol.DirectRequest
		if plan == nil {
			req = l.request(t, i, previous)
		} else {
			req.Tx = plan[i]
			if previous != nil {
				req.InputCertificates = []protocol.InputCertificate{{Certificate: previous.Certificate, Index: 0}}
			}
		}
		start := time.Now()
		cert, err := l.collector.CollectDirect(context.Background(), req)
		must(t, err)
		must(t, l.wallets[1-i%2].ReceiveDirect(req.Tx.Body.Outputs[0], cert, 0))
		r.ReadyMS = append(r.ReadyMS, float64(time.Since(start).Nanoseconds())/1e6)
		p := protocol.DirectPayment{Tx: req.Tx, Certificate: cert, InputCertificates: req.InputCertificates}
		payments = append(payments, p)
		if scenario == "warm" {
			start = time.Now()
			l.install(t, p)
			r.InstallMS += float64(time.Since(start).Nanoseconds()) / 1e6
		}
	}
	r.ForegroundWallMS = float64(time.Since(started).Nanoseconds()) / 1e6
	var ids []byte
	for _, p := range payments {
		id := p.Tx.ID()
		ids = append(ids, id[:]...)
	}
	r.WorkloadHash = protocol.Digest("SAME_ORG_WORKLOAD", ids).String()
	if scenario == "cold" {
		start := time.Now()
		for _, p := range payments {
			l.install(t, p)
		}
		r.InstallMS = float64(time.Since(start).Nanoseconds()) / 1e6
	}
	r.RequestBytes = l.bytes.Load()
	r.Requests = l.requests.Load()
	r.ReferenceMisses = l.misses.Load()
	for _, m := range l.members {
		s := m.DirectCacheStats()
		r.CacheHits += s.VerificationHits
		r.CacheMisses += s.VerificationMisses
	}
	sorted := append([]float64(nil), r.ReadyMS...)
	sort.Float64s(sorted)
	r.ReadyP50MS = sorted[len(sorted)/2]
	r.ReadyP95MS = sorted[(len(sorted)*95-1)/100]
	for _, x := range sorted {
		r.ReadyTotalMS += x
	}
	r.ReadyMeanMS = r.ReadyTotalMS / float64(n)
	r.CALPeak = l.quota(t, protocol.ResourceCAL)
	// The existing durable outbox scans fact IDs, not transaction dependencies.
	sort.Slice(payments, func(i, j int) bool {
		return bytes.Compare(payments[i].Certificate.QC.Fact[:], payments[j].Certificate.QC.Fact[:]) < 0
	})
	if v.order {
		var err error
		payments, err = gateway.OrderDirectSourcesFirst(payments)
		must(t, err)
	}
	l.replay(t, payments, &r)
	return r
}

func (l *lab) replay(t *testing.T, payments []protocol.DirectPayment, r *result) {
	t.Helper()
	ledger := store.NewMemory()
	defer ledger.Close()
	engine, err := committee.NewEngine(committee.EngineConfig{Network: l.f.Org.Network, Organizations: []protocol.OrgConfig{l.f.Org}, Schedule: l.f.Schedule, Genesis: l.f.Genesis, Direct: &l.settings, Accounts: []committee.GenesisAccount{{Owner: l.f.Org.Org, Asset: protocol.AssetCAL, Balance: 1_000_000_000}, {Owner: l.f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1_000_000_000}}}, ledger)
	must(t, err)
	var previous []byte
	start := time.Now()
	for i, p := range payments {
		raw, err := p.Submission().MarshalBinary()
		must(t, err)
		var tr state.Transition
		err = ledger.Update(func(v state.ReadView) ([]state.Change, error) {
			var err error
			tr, err = engine.ExecuteAt(v, raw, committee.BlockContext{Height: int64(i + 1), Time: time.Unix(1700000000+int64(i+1), 0)})
			return tr.Changes, err
		})
		must(t, err)
		executed, err := protocol.DecodeExecution(tr.Data)
		must(t, err)
		if !executed.Applied {
			t.Fatal("payment not applied")
		}
		r.MissingSources += len(executed.MissingInputs)
		trust, data := testkit.Block(chain, int64(i+1), previous, [][]byte{raw}, []*abci.ExecTxResult{{Data: tr.Data}})
		block, err := finality.VerifyBlock(trust, data)
		must(t, err)
		for j, m := range l.members {
			must(t, blockfollow.Commit(l.dbs[j], block, m.PrepareBlock))
		}
		previous = block.Hash()
		cal, fuel := l.quota(t, protocol.ResourceCAL), l.quota(t, protocol.ResourceFUEL)
		var gap uint64
		must(t, ledger.View(func(v state.ReadView) error {
			var e error
			gap, _, e = state.Load[uint64](v, rules.DirectGapKey())
			return e
		}))
		r.CALSteps = append(r.CALSteps, cal)
		r.FUELSteps = append(r.FUELSteps, fuel)
		r.GapSteps = append(r.GapSteps, gap)
		if cal > r.CALPeak {
			r.CALPeak = cal
		}
		if gap > r.GapPeak {
			r.GapPeak = gap
		}
		r.CALFinal = cal
		r.FUELFinal = fuel
		r.GapFinal = gap
	}
	r.PublicProcessingMS = float64(time.Since(start).Nanoseconds()) / 1e6
	if r.CALFinal != 0 || r.GapFinal != 0 {
		t.Fatalf("unclosed payments: %+v", r)
	}
}

func TestSameOrgWarmColdAndOrdering(t *testing.T) {
	for _, scenario := range []string{"warm", "cold"} {
		t.Run(scenario, func(t *testing.T) {
			r := run(t, variants[4], scenario, 4, nil)
			if scenario == "warm" && r.ReferenceMisses != 0 {
				t.Fatal("warm reference missed", r.ReferenceMisses)
			}
			if scenario == "cold" && r.ReferenceMisses == 0 {
				t.Fatal("cold fallback not exercised")
			}
			if r.MissingSources != 0 {
				t.Fatal("source-first produced gaps")
			}
		})
	}
}

func TestSameOrgExperiment(t *testing.T) {
	path := os.Getenv("NEXT_SAME_ORG_RESULTS")
	if path == "" {
		t.Skip("opt-in measurement")
	}
	n, rounds := 100, 3
	if v, err := strconv.Atoi(os.Getenv("NEXT_SAME_ORG_COUNT")); err == nil && v > 0 {
		n = v
	}
	if v, err := strconv.Atoi(os.Getenv("NEXT_SAME_ORG_ROUNDS")); err == nil && v > 0 {
		rounds = v
	}
	var results []result
	var plan []protocol.FastTx
	t.Run("prepare-identical-signed-workload", func(t *testing.T) {
		l := newLab(t, variants[0])
		var previous *protocol.DirectPayment
		for i := 0; i < n; i++ {
			req := l.request(t, i, previous)
			cert, err := l.f.DirectCertificate(req.Tx, l.policy)
			must(t, err)
			plan = append(plan, req.Tx)
			previous = &protocol.DirectPayment{Tx: req.Tx, Certificate: cert, InputCertificates: req.InputCertificates}
		}
	})
	for round := 0; round < rounds; round++ {
		for _, scenario := range []string{"warm", "cold"} {
			for offset := range variants {
				v := variants[(offset+round)%len(variants)]
				t.Run(fmt.Sprintf("r%d-%s-%s", round+1, scenario, v.name), func(t *testing.T) {
					r := run(t, v, scenario, n, plan)
					r.Round = round + 1
					results = append(results, r)
					t.Logf("ready p50=%.3fms bytes=%d CALpeak=%d gaps=%d", r.ReadyP50MS, r.RequestBytes, r.CALPeak, r.MissingSources)
				})
			}
		}
	}
	raw, err := json.MarshalIndent(results, "", "  ")
	must(t, err)
	must(t, os.MkdirAll(filepath.Dir(path), 0755))
	must(t, os.WriteFile(path, raw, 0644))
}
