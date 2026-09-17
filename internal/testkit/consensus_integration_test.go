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
	fixture := NewFixture(chain, "a", 1)
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
		c.Consensus.CreateEmptyBlocks = true
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
		apps[i], err = committee.NewApp(chain, db, engine.Check, engine.Execute)
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
	t.Logf("four independent bbolt apps committed at height %d; proof authenticated by header %d", height, proofHeight)
}
