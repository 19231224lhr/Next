package member_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	abci "github.com/cometbft/cometbft/abci/types"
	"os"
	"reflect"
	"testing"
	"time"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/committee"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestDirectWalletBindingsAcrossOrganizations(t *testing.T) {
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
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "a_first", true: "b_first"}[reverse], func(t *testing.T) {
			a, b := testkit.NewFixture("wallet-binding", "a", 1), testkit.NewFixture("wallet-binding", "b", 1)
			a.EnableDirect()
			b.EnableDirect()
			orgs := []protocol.OrgConfig{a.Org, b.Org}
			policy, err := settings.Policy(a.Schedule, orgs)
			if err != nil {
				t.Fatal(err)
			}
			genesis := a.Genesis
			genesis.Outputs = append(genesis.Outputs, b.Genesis.Outputs...)
			genesis.Grants = append(genesis.Grants, b.Genesis.Grants...)
			ledger := store.NewMemory()
			t.Cleanup(func() { ledger.Close() })
			var accounts []committee.GenesisAccount
			for _, org := range orgs {
				for _, asset := range []protocol.Asset{protocol.AssetCAL, protocol.AssetFUEL} {
					accounts = append(accounts, committee.GenesisAccount{Owner: org.Org, Asset: asset, Balance: 1_000_000_000})
				}
			}
			engine, err := committee.NewEngine(committee.EngineConfig{Network: a.Org.Network, Organizations: orgs, Schedule: a.Schedule, Genesis: genesis, Direct: &settings, Accounts: accounts}, ledger)
			if err != nil {
				t.Fatal(err)
			}
			var payments [2]protocol.DirectPayment
			var signers [2][]*member.Member
			var stores [2][]store.Store
			for orgIndex, f := range []testkit.Fixture{a, b} {
				tx, err := f.FastTransaction(0, 1, policy)
				if err != nil {
					t.Fatal(err)
				}
				certificate := protocol.OutputCertificate{}
				for i := 0; i < 3; i++ {
					db := store.NewMemory()
					t.Cleanup(func() { db.Close() })
					m, err := member.New(member.Config{Organization: f.Org, Index: uint16(i), Key: f.Keys[i], Peers: orgs, Schedule: f.Schedule, Workers: 1, Direct: &settings}, db, genesis)
					if err != nil {
						t.Fatal(err)
					}
					body := tx.Body
					other := []testkit.Fixture{b, a}[orgIndex]
					body.Subject = other.Genesis.Outputs[0].Output.Recipient.Owner
					forged, err := protocol.NewFastTx(body, tx.Claims, policy.Key)
					if err != nil {
						t.Fatal(err)
					}
					forged.Auth = []protocol.OwnerAuth{protocol.SignOwner(forged.ID(), f.Owner)}
					if _, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: forged}); err == nil {
						t.Fatal("foreign subject impersonation approved")
					}
					vote, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx})
					if err != nil {
						t.Fatal(err)
					}
					certificate.Summary, certificate.QC.Fact = vote.Summary, vote.Summary.Fact()
					certificate.QC.Votes = append(certificate.QC.Votes, vote.Vote)
					signers[orgIndex] = append(signers[orgIndex], m)
					stores[orgIndex] = append(stores[orgIndex], db)
				}
				if err := certificate.Verify(f.Org); err != nil {
					t.Fatal(err)
				}
				payments[orgIndex] = protocol.DirectPayment{Tx: tx, Certificate: certificate}
			}
			first, second := 0, 1
			if reverse {
				first, second = second, first
			}
			var previous []byte
			execute := func(index int, height int64) {
				raw, err := payments[index].Submission().MarshalBinary()
				if err != nil {
					t.Fatal(err)
				}
				var result []byte
				err = ledger.Update(func(v state.ReadView) ([]state.Change, error) {
					tr, e := engine.ExecuteAt(v, raw, committee.BlockContext{Height: height, Time: time.Unix(100, 0)})
					result = tr.Data
					return tr.Changes, e
				})
				if err != nil {
					t.Fatal("independent bound wallet payment blocked", err)
				}
				trust, data := testkit.Block("wallet-binding", height, previous, [][]byte{raw}, []*abci.ExecTxResult{{Data: result}})
				verified, err := finality.VerifyBlock(trust, data)
				if err != nil {
					t.Fatal(err)
				}
				for j, members := range signers {
					for i, m := range members {
						if err := blockfollow.Commit(stores[j][i], verified, m.PrepareBlock); err != nil {
							t.Fatal(err)
						}
					}
				}
				previous = verified.Hash()
				before, err := store.Scan(ledger, nil, nil, 1000)
				if err != nil {
					t.Fatal(err)
				}
				if err := ledger.Update(func(v state.ReadView) ([]state.Change, error) {
					tr, e := engine.ExecuteAt(v, raw, committee.BlockContext{Height: height + 1, Time: time.Unix(100, 0)})
					return tr.Changes, e
				}); err != nil {
					t.Fatal(err)
				}
				after, err := store.Scan(ledger, nil, nil, 1000)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("public retry changed accounting", err)
				}
			}
			execute(first, 1)
			execute(second, 2)
			for j, members := range signers {
				conflict, err := []testkit.Fixture{a, b}[j].FastTransaction(0, 2, policy)
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range members {
					if _, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: conflict}); !errors.Is(err, rules.ErrConflict) {
						t.Fatal("same input produced another approval", err)
					}
				}
			}
			contradictory := genesis
			contradictory.Outputs = append([]state.OriginOutput(nil), genesis.Outputs...)
			contradictory.Outputs[1].Output.Recipient = protocol.NewDescriptor(a.Org.Network, protocol.Route{Kind: protocol.OrgRoute, Org: b.Org.Org}, a.Owner)
			db := store.NewMemory()
			t.Cleanup(func() { db.Close() })
			if _, err := member.New(member.Config{Organization: a.Org, Key: a.Keys[0], Peers: orgs, Schedule: a.Schedule, Workers: 1, Direct: &settings}, db, contradictory); err == nil {
				t.Fatal("one owner bound to two organizations")
			}
		})
	}
}
