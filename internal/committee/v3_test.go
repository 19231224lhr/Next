package committee_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	abci "github.com/cometbft/cometbft/abci/types"
	"os"
	"testing"
	"time"
	"utxo/internal/committee"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestDirectABCIChildBeforeParentReplay(t *testing.T) {
	f := testkit.NewFixture("v3-app", "org", 1)
	f.EnableDirect()
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	settings := rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	p, err := settings.Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if err != nil {
		t.Fatal(err)
	}
	cfg := committee.EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Direct: &settings, Accounts: []committee.GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetCAL, Balance: 1000000000}, {Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1000000000}}}
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
	payments := []protocol.DirectPayment{{Tx: child, Certificate: cc, InputCertificates: []protocol.InputCertificate{{Certificate: pc, Index: 0}}}, {Tx: parent, Certificate: pc}}
	var reference [][]byte
	for replica := 0; replica < 2; replica++ {
		db := store.NewMemory()
		defer db.Close()
		engine, err := committee.NewEngine(cfg, db)
		if err != nil {
			t.Fatal(err)
		}
		app, err := committee.NewTimedApp("v3-app", db, engine.Check, engine.ExecuteAt, engine.BeginBlock)
		if err != nil {
			t.Fatal(err)
		}
		for i, payment := range payments {
			raw, err := payment.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			req := &abci.RequestFinalizeBlock{Height: int64(i + 1), Hash: bytes.Repeat([]byte{byte(i + 1)}, 32), Time: time.Unix(int64(100+i), 0), Txs: [][]byte{raw}}
			result, err := app.FinalizeBlock(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if result.TxResults[0].Code != 0 {
				t.Fatalf("rejected: %+v", result.TxResults[0])
			}
			if _, err = app.Commit(context.Background(), &abci.RequestCommit{}); err != nil {
				t.Fatal(err)
			}
			if replica == 0 {
				reference = append(reference, bytes.Clone(result.AppHash))
			} else if !bytes.Equal(reference[i], result.AppHash) {
				t.Fatal("replay app hash differs")
			}
			if i == 0 {
				err = db.View(func(v state.ReadView) error {
					created, found, err := state.Load[state.Creation](v, rules.DirectCreationKey(cc.Summary.OutputID(0), 0))
					if !found || !created.Final {
						t.Fatal("child did not finalize")
					}
					ob, found, err := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(pc.Summary.OutputID(0)))
					if !found || ob.Deadline != 0 || ob.AnchorHeight != 2 {
						t.Fatal("deadline is not based on consensus time")
					}
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
