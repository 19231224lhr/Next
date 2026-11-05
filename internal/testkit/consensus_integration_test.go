//go:build integration

package testkit

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cfg "github.com/cometbft/cometbft/config"
	cmted "github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/p2p"
	"github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/proxy"
	"github.com/cometbft/cometbft/rpc/client/local"
	ct "github.com/cometbft/cometbft/types"
	"utxo/finality"
	"utxo/internal/committee"
	"utxo/internal/member"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestFourNodePaymentSettlementAndCredit(t *testing.T) {
	const chain = "utxo-integration"
	var diagnostics bytes.Buffer
	logger := log.NewFilter(log.NewTMLogger(log.NewSyncWriter(&diagnostics)), log.AllowError())
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(diagnostics.String())
		}
	})
	network := protocol.Digest("NETWORK", []byte(chain))
	fixture := NewFixture(chain, "a", 2)
	tx := fixture.Transaction(0, 1)
	certificate, err := fixture.Certify(tx)
	if err != nil {
		t.Fatal(err)
	}
	command, err := certificate.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	receipt := protocol.CreditReceipt{Spend: certificate.QC.Fact, Resource: fixture.Genesis.Grants[0].Key, Original: 100, Paid: 94, Discharged: 6, Revision: 1}
	payload, _ := receipt.MarshalBinary()
	fact := protocol.FinalFact{Kind: protocol.FactCredit, Key: receipt.Key(), Revision: 1, Network: network, Rules: fixture.Schedule.IDs(), Payload: payload}
	genesis := &ct.GenesisDoc{ChainID: chain, GenesisTime: time.Now(), InitialHeight: 1, ConsensusParams: ct.DefaultConsensusParams()}
	configs := make([]*cfg.Config, 4)
	pvs := make([]*privval.FilePV, 4)
	nks := make([]*p2p.NodeKey, 4)
	addrs := make([]string, 4)
	validators := make([]*ct.Validator, 4)
	for i := 0; i < 4; i++ {
		c := cfg.DefaultConfig().SetRoot(filepath.Join(t.TempDir(), fmt.Sprint(i)))
		cfg.EnsureRoot(c.RootDir)
		c.Moniker = fmt.Sprintf("test-%d", i)
		c.RPC.ListenAddress = ""
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addrs[i] = listener.Addr().String()
		listener.Close()
		c.P2P.ListenAddress = "tcp://" + addrs[i]
		c.P2P.AllowDuplicateIP = true
		c.P2P.AddrBookStrict = false
		c.P2P.PexReactor = false
		c.Consensus.TimeoutPropose = 500 * time.Millisecond
		c.Consensus.TimeoutPrevote = 100 * time.Millisecond
		c.Consensus.TimeoutPrecommit = 100 * time.Millisecond
		c.Consensus.TimeoutCommit = 100 * time.Millisecond
		c.Consensus.CreateEmptyBlocks = false
		c.Consensus.CreateEmptyBlocksInterval = 0
		c.Instrumentation.Prometheus = false
		key := cmted.GenPrivKey()
		pvs[i] = privval.NewFilePV(key, c.PrivValidatorKeyFile(), c.PrivValidatorStateFile())
		pvs[i].Save()
		nks[i], err = p2p.LoadOrGenNodeKey(c.NodeKeyFile())
		if err != nil {
			t.Fatal(err)
		}
		genesis.Validators = append(genesis.Validators, ct.GenesisValidator{Address: key.PubKey().Address(), PubKey: key.PubKey(), Power: 1, Name: c.Moniker})
		validators[i] = ct.NewValidator(key.PubKey(), 1)
		configs[i] = c
	}
	apps := make([]*committee.App, 4)
	nodes := make([]*node.Node, 4)
	clients := make([]*local.Local, 4)
	// Register database cleanup before node cleanup so nodes stop first.
	for i, c := range configs {
		peers := ""
		for j := 0; j < 4; j++ {
			if i != j {
				if peers != "" {
					peers += ","
				}
				peers += string(nks[j].ID()) + "@" + addrs[j]
			}
		}
		c.P2P.PersistentPeers = peers
		db, err := store.Open(filepath.Join(c.RootDir, "application.db"), store.Identity{Network: chain, Role: "committee", Node: fmt.Sprint(i), Schema: 2})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		engine, err := committee.NewEngine(committee.EngineConfig{Network: network, Organizations: []protocol.OrgConfig{fixture.Org}, Schedule: fixture.Schedule, Genesis: fixture.Genesis, Accounts: []committee.GenesisAccount{{Owner: fixture.Org.Org, Asset: protocol.AssetFUEL, Balance: 1_000_000_000}}}, db)
		if err != nil {
			t.Fatal(err)
		}
		apps[i], err = committee.NewApp(chain, db, engine.Check, engine.Execute, engine.Drain)
		if err != nil {
			t.Fatal(err)
		}
		nodes[i], err = node.NewNode(c, pvs[i], nks[i], proxy.NewLocalClientCreator(apps[i]), func() (*ct.GenesisDoc, error) { return genesis, nil }, cfg.DefaultDBProvider, node.DefaultMetricsProvider(c.Instrumentation), logger)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range nodes {
		n := n
		t.Cleanup(func() {
			if n.IsRunning() {
				n.Stop()
				n.Wait()
			}
		})
	}
	for i, n := range nodes {
		if err := n.Start(); err != nil {
			t.Fatal(err)
		}
		clients[i] = local.New(n)
	}
	assertIdleHeight(t, apps, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var height int64
	for ctx.Err() == nil {
		result, err := clients[0].BroadcastTxSync(ctx, ct.Tx(command))
		if err == nil && result.Code != 0 {
			t.Fatalf("CheckTx: code=%d log=%s", result.Code, result.Log)
		}
		if err == nil && result.Code == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The transaction can be in any height. Query its transaction result then use h+1.
	for ctx.Err() == nil {
		result, err := clients[0].Tx(ctx, ct.Tx(command).Hash(), false)
		if err == nil {
			if result.TxResult.Code != 0 {
				t.Fatalf("execution: %+v", result.TxResult)
			}
			height = result.Height
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if height == 0 {
		for i, app := range apps {
			info, _ := app.Info(context.Background(), &abci.RequestInfo{})
			t.Logf("app %d height=%d root=%X", i, info.LastBlockHeight, info.LastBlockAppHash)
		}
		t.Fatal("transaction did not commit before deadline")
	}
	proofHeight := height + 1
	var header *ct.SignedHeader
	for ctx.Err() == nil {
		result, err := clients[0].Commit(ctx, &proofHeight)
		if err == nil {
			header = &result.SignedHeader
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if header == nil {
		t.Fatal("next-height header unavailable")
	}
	trust := finality.Trust{ChainID: chain, Network: network, Validators: ct.NewValidatorSet(validators)}
	memberDB, err := store.Open(filepath.Join(t.TempDir(), "member.db"), store.Identity{Network: chain, Role: "member", Node: "0", Schema: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer memberDB.Close()
	signingMember, err := member.New(member.Config{Organization: fixture.Org, Index: 0, Key: fixture.Keys[0], Schedule: fixture.Schedule, Workers: 1, Committee: trust}, memberDB, fixture.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = signingMember.Approve(member.Request{Tx: tx}); err != nil {
		t.Fatal(err)
	}
	before, _ := signingMember.Quota(receipt.Resource, 0)

	for i, app := range apps {
		for ctx.Err() == nil {
			info, _ := app.Info(ctx, &abci.RequestInfo{})
			if info.LastBlockHeight >= proofHeight {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		proof, err := app.Proof(fact.ID(), *header)
		if err != nil {
			t.Fatalf("node %d: %v", i, err)
		}
		verified, err := finality.Verify(trust, proof)
		if err != nil {
			t.Fatalf("node %d proof: %v", i, err)
		}
		if err = signingMember.ApplyProof(proof); err != nil {
			t.Fatal(err)
		}
		if verified.Fact().ID() != fact.ID() {
			t.Fatal("wrong fact")
		}
	}
	after, _ := signingMember.Quota(receipt.Resource, 0)
	if after.Available != before.Available+6 || after.Reserved != 94 {
		t.Fatalf("original member quota: before=%+v after=%+v", before, after)
	}
	assertIdleHeight(t, apps, proofHeight)
	checkIdleDependencies(t, fixture, apps, nodes, clients, trust)
	t.Logf("four independent bbolt apps committed at height %d; proof authenticated by header %d", height, proofHeight)
}

// An idle network must retain its last proof block until a real transaction arrives.
func assertIdleHeight(t *testing.T, apps []*committee.App, height int64) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		ready := true
		for _, app := range apps {
			info, err := app.Info(context.Background(), &abci.RequestInfo{})
			if err != nil {
				t.Fatal(err)
			}
			if info.LastBlockHeight > height {
				t.Fatalf("idle network produced unwanted block: got %d, want %d", info.LastBlockHeight, height)
			}
			ready = ready && info.LastBlockHeight == height
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("nodes did not reach expected idle height")
		}
		time.Sleep(25 * time.Millisecond)
	}
	time.Sleep(time.Second)
	for _, app := range apps {
		info, err := app.Info(context.Background(), &abci.RequestInfo{})
		if err != nil {
			t.Fatal(err)
		}
		if info.LastBlockHeight != height {
			t.Fatalf("idle network produced unwanted block: got %d, want %d", info.LastBlockHeight, height)
		}
	}
}

// No wallet or gateway relay runs here: only the explicit broadcasts can wake consensus.
func checkIdleDependencies(t *testing.T, f Fixture, apps []*committee.App, nodes []*node.Node, clients []*local.Local, trust finality.Trust) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root, err := f.Certify(f.Transaction(1, 100))
	if err != nil {
		t.Fatal(err)
	}
	chain := []protocol.TXCer{root}
	for i := 1; i <= committee.MaxDependencyActions+8; i++ {
		parent := chain[len(chain)-1]
		body := f.Transaction(1, uint64(100+i)).Body
		body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: parent.Effects.Outputs[0], Evidence: protocol.Hash(parent.QC.Fact)}}
		child, err := f.Certify(f.Sign(body), parent)
		if err != nil {
			t.Fatal(err)
		}
		chain = append(chain, child)
	}
	submit := func(c protocol.TXCer) []byte {
		raw, err := c.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		result, err := clients[0].BroadcastTxSync(ctx, ct.Tx(raw))
		if err != nil {
			t.Fatal(err)
		}
		if result.Code != 0 {
			t.Fatalf("CheckTx rejected: %+v", result)
		}
		return raw
	}
	committed := func(raw []byte) int64 {
		for ctx.Err() == nil {
			result, err := clients[0].Tx(ctx, ct.Tx(raw).Hash(), false)
			if err == nil {
				if result.TxResult.Code != 0 {
					t.Fatalf("execution rejected: %+v", result.TxResult)
				}
				return result.Height
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("command did not commit")
		return 0
	}
	var children [][]byte
	for i := len(chain) - 1; i >= 1; i-- {
		children = append(children, submit(chain[i]))
	}
	var lastRegistration int64
	for _, raw := range children {
		if h := committed(raw); h > lastRegistration {
			lastRegistration = h
		}
	}
	assertIdleHeight(t, apps, lastRegistration+1)
	for i, n := range nodes {
		if n.Mempool().Size() != 0 {
			t.Fatalf("node %d still has transactions before root", i)
		}
	}
	last := chain[len(chain)-1]
	if _, err := apps[0].LatestFact(protocol.FactOutputCreated, protocol.Hash(last.Effects.Outputs[0])); err == nil {
		t.Fatal("missing root unexpectedly settled")
	}
	rootHeight := committed(submit(root))
	// Root wakes 40 children. Only 32 can drain in its block; the rest need a transaction-free block.
	assertIdleHeight(t, apps, rootHeight+2)
	for i, n := range nodes {
		if n.Mempool().Size() != 0 {
			t.Fatalf("node %d has unexpected wakeup transactions", i)
		}
		block, err := clients[i].Block(ctx, ptrHeight(rootHeight+1))
		if err != nil {
			t.Fatal(err)
		}
		if len(block.Block.Txs) != 0 {
			t.Fatal("maintenance was woken by another transaction")
		}
		fact, err := apps[i].LatestFact(protocol.FactOutputCreated, protocol.Hash(last.Effects.Outputs[0]))
		if err != nil {
			t.Fatal(err)
		}
		h, err := apps[i].FactHeight(fact.ID())
		if err != nil || h != rootHeight+1 {
			t.Fatalf("node %d tail height=%d err=%v", i, h, err)
		}
		header, err := clients[i].Commit(ctx, ptrHeight(h+1))
		if err != nil {
			t.Fatal(err)
		}
		proof, err := apps[i].Proof(fact.ID(), header.SignedHeader)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = finality.Verify(trust, proof); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("40 waiting children drained across heights %d and %d with no relay; final proof at %d, then idle", rootHeight, rootHeight+1, rootHeight+2)
}
func ptrHeight(h int64) *int64 { return &h }
