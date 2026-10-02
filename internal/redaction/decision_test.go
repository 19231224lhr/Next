//go:build comet_v3

package redaction_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
	"time"
	"utxo/internal/redaction"

	dbm "github.com/cometbft/cometbft-db"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtstore "github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
	apppkg "utxo/internal/committee"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

// This test deliberately withholds every adaptation share. Economic closure
// must still be a publicly executable command, not a local helper invocation.
func TestCompensationBeforeAdaptation(t *testing.T) {
	_, _, vals, keys := committee(t)
	f := testkit.NewFixture(chain, "decision", 1)
	f.EnableDirect()
	keyBytes, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	pemBlock, _ := pem.Decode(keyBytes)
	key, err := x509.ParsePKCS1PrivateKey(pemBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	settings := rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	p, err := settings.Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if err != nil {
		t.Fatal(err)
	}
	db := store.NewMemory()
	defer db.Close()
	bs := cmtstore.NewBlockStore(dbm.NewMemDB())
	e, err := apppkg.NewEngine(apppkg.EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Direct: &settings, Accounts: []apppkg.GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetCAL, Balance: 1_000_000_000}, {Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1_000_000_000}}}, db)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.EnableRepair(bs); err != nil {
		t.Fatal(err)
	}
	app, err := apppkg.NewTimedApp(chain, db, e.Check, e.ExecuteAt, e.BeginBlock)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := f.FastTransaction(0, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := f.DirectCertificate(parent, p)
	if err != nil {
		t.Fatal(err)
	}
	body := parent.Body
	body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: pc.Summary.OutputID(0), Evidence: protocol.Hash(pc.QC.Fact)}}
	body.Nonce[0] = 42
	body.Intent = body.IntentID()
	child, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: parent.Body.Outputs[0]}}, p.Key)
	if err != nil {
		t.Fatal(err)
	}
	child.Auth = []protocol.OwnerAuth{protocol.SignOwner(child.ID(), f.Owner)}
	cc, err := f.DirectCertificate(child, p)
	if err != nil {
		t.Fatal(err)
	}
	childRaw, err := (protocol.DirectPayment{Tx: child, Certificate: cc, InputCertificates: []protocol.InputCertificate{{Certificate: pc}}}).Submission().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	last := &types.Commit{}
	appendBlock := func(height, stamp int64, raw []byte) []byte {
		t.Helper()
		b := block(t, height, raw, last, vals)
		b.Time = time.Unix(stamp, 0).UTC()
		parts, err := b.MakePartSet(types.BlockPartSizeBytes)
		if err != nil {
			t.Fatal(err)
		}
		last = commit(t, height, types.BlockID{Hash: b.Hash(), PartSetHeader: parts.Header()}, vals, keys)
		bs.SaveBlock(b, parts, last)
		r, err := app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: height, Hash: b.Hash(), Time: b.Time, Txs: [][]byte{raw}})
		if err != nil {
			t.Fatal(err)
		}
		if r.TxResults[0].Code != 0 {
			t.Fatalf("public execution failed: %+v", r.TxResults[0])
		}
		if _, err = app.Commit(context.Background(), &abci.RequestCommit{}); err != nil {
			t.Fatal(err)
		}
		return r.TxResults[0].Data
	}
	appendBlock(1, 1700000001, childRaw)
	appendBlock(2, 1700000002, protocol.ClockTick(f.Org.Network, 2))
	originalID := bs.LoadBlockMeta(1).BlockID
	// Canonical decision wire: no input or block adaptation witness.
	out := pc.Summary.OutputID(0)
	wire := new(protocol.Encoder)
	wire.Fixed([]byte("CMPDEC05"))
	wire.Fixed(f.Org.Network[:])
	wire.Fixed(out[:])
	wire.U64(1)
	wire.U32(0)
	wire.U32(0)
	if err := e.Check(wire.Data()); err != nil {
		t.Fatalf("independent compensation command rejected: %v", err)
	}
	decision, err := protocol.DecodeCompensationDecision(wire.Data())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.View(func(v state.ReadView) error {
		for _, name := range []string{"early", "network", "height", "transaction", "input", "amount", "balance", "no-obligation", "source-first"} {
			t.Run("guard_"+name, func(t *testing.T) {
				o := state.NewOverlay(v)
				c, now := decision, int64(1700000032)
				switch name {
				case "early":
					now = 1700000030
				case "network":
					c.Network[0] ^= 1
				case "height":
					c.Height++
				case "transaction":
					c.Transaction++
				case "input":
					c.Input++
				case "amount":
					ob, _, err := state.Load[rules.DirectObligation](o, rules.DirectObligationKey(out))
					if err != nil {
						t.Fatal(err)
					}
					ob.Amount++
					if err := state.Put(o, rules.DirectObligationKey(out), ob); err != nil {
						t.Fatal(err)
					}
				case "balance":
					if err := state.Put(o, rules.AccountKey(f.Org.Org, protocol.AssetCAL), uint64(99)); err != nil {
						t.Fatal(err)
					}
				case "no-obligation":
					o.Delete(rules.DirectObligationKey(out))
				case "source-first":
					verified, err := rules.VerifyDirectPayment(protocol.DirectPayment{Tx: parent, Certificate: pc}, p)
					if err != nil {
						t.Fatal(err)
					}
					tr, err := rules.EvaluateDirectPayment(o, verified, p, now)
					if err != nil {
						t.Fatal(err)
					}
					o.Apply(tr.Changes)
				}
				tr, err := redaction.ExecuteDecision(o, bs, p, c, 3, now)
				if err == nil || len(tr.Changes) != 0 || len(tr.Data) != 0 {
					t.Fatal("invalid decision produced effects", err)
				}
			})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data := appendBlock(3, 1700000032, wire.Data())
	result, effects, err := protocol.DecodePublicExecution(f.Org.Network, wire.Data(), data)
	if err != nil || !result.Applied || len(effects) != 1 || effects[0].Amount != 100 {
		t.Fatalf("missing public compensation effect: %+v %+v %v", result, effects, err)
	}
	if err := db.View(func(v state.ReadView) error {
		balance, _, err := state.Load[uint64](v, rules.AccountKey(f.Org.Org, protocol.AssetCAL))
		if balance != 999999900 {
			t.Fatalf("balance=%d", balance)
		}
		ob, _, err := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(out))
		if ob.Status != rules.DirectRepaired {
			t.Fatal("gap not closed")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bs.LoadBlock(1).Data.Txs[0], childRaw) || !bs.LoadBlockMeta(1).BlockID.Equals(originalID) {
		t.Fatal("decision changed historical representation")
	}
	data = appendBlock(4, 1700000033, wire.Data())
	result, effects, err = protocol.DecodePublicExecution(f.Org.Network, wire.Data(), data)
	if err != nil || result.Applied || len(effects) != 0 {
		t.Fatal("duplicate compensation has effects", err)
	}
	parentRaw, err := (protocol.DirectPayment{Tx: parent, Certificate: pc}).Submission().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	appendBlock(5, 1700000034, parentRaw)
	if err := db.View(func(v state.ReadView) error {
		// Recovery returns real funds but never reopens the debit decision.
		tr, err := redaction.ExecuteDecision(v, bs, p, decision, 6, 1700000035)
		if err != nil || len(tr.Changes) != 0 {
			t.Fatal("decision replay after recovery changed state", err)
		}
		result, effects, err := protocol.DecodePublicExecution(f.Org.Network, wire.Data(), tr.Data)
		if err != nil || result.Applied || len(effects) != 0 || len(result.FeeOutputs) != 0 {
			t.Fatal("decision replay after recovery repeated economic effects", err)
		}
		conflict := decision
		conflict.Transaction++
		tr, err = redaction.ExecuteDecision(v, bs, p, conflict, 6, 1700000035)
		if err == nil || len(tr.Changes) != 0 || len(tr.Data) != 0 {
			t.Fatal("conflicting location reused an existing decision", err)
		}
		balance, _, err := state.Load[uint64](v, rules.AccountKey(f.Org.Org, protocol.AssetCAL))
		if balance != 1_000_000_000 {
			t.Fatalf("late source did not repay: %d", balance)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
