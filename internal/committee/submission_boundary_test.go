package committee

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"
	"utxo/internal/rules"

	abci "github.com/cometbft/cometbft/abci/types"
	"utxo/internal/store"
	"utxo/protocol"
)

// Exercise received bytes, not the encoder's refusal to create a bad request.
func TestPublicAuthorizationAtABCIEntry(t *testing.T) {
	cfg, fixture, policy, valid := cacheFixture(t, 2)
	base, err := protocol.DecodeDirectSubmission(valid[0])
	if err != nil {
		t.Fatal(err)
	}
	other, err := protocol.DecodeDirectSubmission(valid[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"unrelated-qc", "forged-qc", "forged-owner"} {
		t.Run(kind, func(t *testing.T) {
			p, err := protocol.DecodeDirectSubmission(valid[0])
			if err != nil {
				t.Fatal(err)
			}
			var raw []byte
			switch kind {
			case "unrelated-qc":
				raw = bytes.Clone(valid[0])
				replace := func(from, to []byte) {
					if len(from) != len(to) || bytes.Count(raw, from) != 1 {
						t.Fatal("ambiguous wire field replacement")
					}
					copy(raw[bytes.Index(raw, from):], to)
				}
				replace(base.Authorization.Fact[:], other.Authorization.Fact[:])
				for i, vote := range base.Authorization.Votes {
					if vote.Member != other.Authorization.Votes[i].Member {
						t.Fatal("fixture signer mismatch")
					}
					replace(vote.Signature[:], other.Authorization.Votes[i].Signature[:])
				}
			case "forged-qc":
				p.Authorization.Votes[0].Signature[0] ^= 1
			case "forged-owner":
				pos := bytes.Index(valid[0], base.Tx.Auth[0].Signature[:])
				if pos < 0 {
					t.Fatal("owner signature absent from public bytes")
				}
				raw = bytes.Clone(valid[0])
				raw[pos] ^= 1
			}
			if raw == nil {
				raw, err = p.MarshalBinary()
				if err != nil {
					t.Fatal("test must reach inbound verification", err)
				}
			}
			// Isolate the cryptographic QC rejection from encoding, owner,
			// configuration and fact-binding failures. Other cases deliberately
			// exercise earlier boundaries and make no such depth claim.
			if kind == "forged-qc" {
				decoded, err := protocol.DecodeDirectSubmission(raw)
				if err != nil {
					t.Fatal("QC negative must decode", err)
				}
				vector, err := rules.PrepareDirectVector(decoded.Tx, policy)
				if err != nil {
					t.Fatal("QC negative must pass owner and initial-funding validation", err)
				}
				summary := decoded.Summary()
				if summary.Validate() != nil || summary.Network != fixture.Org.Network || summary.Config != fixture.Org.Hash() || summary.Issuer != fixture.Org.Org || summary.Epoch != fixture.Org.Epoch || summary.Fact() != decoded.Authorization.Fact || summary.Fact() != protocol.SummaryFor(decoded.Tx, vector).Fact() {
					t.Fatal("QC negative has an unrelated binding failure")
				}
				if protocol.VerifyQC(decoded.Authorization, fixture.Org) == nil {
					t.Fatal("forged signature verified")
				}
				if _, err := rules.VerifyDirectSubmission(decoded, policy); err != protocol.ErrAuth {
					t.Fatalf("expected QC authentication rejection, got %v", err)
				}
			}
			db := store.NewMemory()
			defer db.Close()
			engine, err := NewEngine(cfg, db)
			if err != nil {
				t.Fatal(err)
			}
			app, err := NewTimedApp("verify-cache", db, engine.Check, engine.ExecuteAt)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			before := cacheLedger(t, db)
			// Warm the exact-byte verification cache with the valid original.
			ok, err := app.CheckTx(ctx, &abci.RequestCheckTx{Tx: valid[0]})
			if err != nil || ok.Code != 0 {
				t.Fatal("valid control rejected", err)
			}
			check, err := app.CheckTx(ctx, &abci.RequestCheckTx{Tx: raw})
			if err != nil || check.Code == 0 {
				t.Fatal("invalid public authorization entered mempool", err)
			}
			proposal, err := app.ProcessProposal(ctx, &abci.RequestProcessProposal{Height: 1, Txs: [][]byte{raw}})
			if err != nil || proposal.Status != abci.ResponseProcessProposal_REJECT {
				t.Fatal("invalid public authorization entered proposal", err)
			}
			// Direct invocation also cannot bypass the final execution gate.
			final, err := app.FinalizeBlock(ctx, &abci.RequestFinalizeBlock{Height: 1, Time: time.Unix(100, 0), Hash: bytes.Repeat([]byte{1}, 32), Txs: [][]byte{raw}})
			if err != nil || len(final.TxResults) != 1 || final.TxResults[0].Code == 0 {
				t.Fatal("invalid public authorization executed", err)
			}
			if len(app.pendingChanges) != 0 || !reflect.DeepEqual(before, cacheLedger(t, db)) {
				t.Fatal("rejected request changed business state")
			}
		})
	}
}
