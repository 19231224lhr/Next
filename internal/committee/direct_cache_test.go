package committee

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	abci "github.com/cometbft/cometbft/abci/types"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func cacheFixture(t testing.TB, count int) (EngineConfig, testkit.Fixture, rules.DirectPolicy, [][]byte) {
	t.Helper()
	f := testkit.NewFixture("verify-cache", "org", count)
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
	cfg := EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Direct: &settings, Accounts: []GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetCAL, Balance: 1000000000}, {Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1000000000}}}
	var payments [][]byte
	for i := 0; i < count; i++ {
		tx, err := f.FastTransaction(i, uint64(i+1), p)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := f.DirectCertificate(tx, p)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := (protocol.DirectPayment{Tx: tx, Certificate: cert}).Submission().MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		payments = append(payments, raw)
	}
	return cfg, f, p, payments
}

func TestDirectVerificationCacheConcurrentAndFrozen(t *testing.T) {
	cfg, f, _, txs := cacheFixture(t, 1)
	db := store.NewMemory()
	defer db.Close()
	e, err := NewEngine(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	saved := bytes.Clone(txs[0])
	payment, err := protocol.DecodeDirectSubmission(saved)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.Check(txs[0]); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	_, _, _, _, entries, size := e.directCache.stats()
	if entries != 1 || size != len(saved) {
		t.Fatal("concurrent misses duplicated cache state")
	}
	clear(txs[0])
	verified, err := e.verifyV3(saved)
	if err != nil || verified.TxID() != payment.Tx.ID() {
		t.Fatal("caller buffer mutated cached object", err)
	}
	// A different valid quorum over the same identity is a different cache entry.
	payment.Authorization.Votes[2] = protocol.SignSpend(payment.Authorization.Fact, 3, f.Keys[3])
	alternate, err := payment.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Check(alternate); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, entries, _ = e.directCache.stats()
	if entries != 2 {
		t.Fatal("cache key omitted authorization bytes")
	}
}

func cacheLedger(t testing.TB, db store.Store) []state.Entry {
	t.Helper()
	var result []state.Entry
	var cursor []byte
	for {
		page, err := store.Scan(db, nil, cursor, 1024)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			return result
		}
		result = append(result, page...)
		cursor = page[len(page)-1].Key
	}
}

func TestDirectVerificationCacheStillChecksLedger(t *testing.T) {
	cfg, f, policy, txs := cacheFixture(t, 1)
	conflict, err := f.FastTransaction(0, 999, policy)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := f.DirectCertificate(conflict, policy)
	if err != nil {
		t.Fatal(err)
	}
	conflictRaw, err := (protocol.DirectPayment{Tx: conflict, Certificate: cert}).Submission().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"spent", "grant"} {
		t.Run(mode, func(t *testing.T) {
			db := store.NewMemory()
			defer db.Close()
			e, err := NewEngine(cfg, db)
			if err != nil {
				t.Fatal(err)
			}
			for _, raw := range [][]byte{txs[0], conflictRaw} {
				if err := e.Check(raw); err != nil {
					t.Fatal(err)
				}
			}
			apply := func(raw []byte) error {
				return db.Update(func(v state.ReadView) ([]state.Change, error) {
					tr, err := e.ExecuteAt(v, raw, BlockContext{Height: 1, Time: time.Unix(100, 0)})
					return tr.Changes, err
				})
			}
			if mode == "spent" {
				if err := apply(txs[0]); err != nil {
					t.Fatal(err)
				}
				before := cacheLedger(t, db)
				if err := apply(txs[0]); err != nil {
					t.Fatal("duplicate payment rejected", err)
				}
				after := cacheLedger(t, db)
				if !reflect.DeepEqual(before, after) {
					t.Fatal("duplicate changed state")
				}
				if err := apply(conflictRaw); err == nil {
					t.Fatal("warm cache authorized double spend")
				}
			} else {
				err := db.Update(func(v state.ReadView) ([]state.Change, error) {
					o := state.NewOverlay(v)
					for _, g := range cfg.Genesis.Grants {
						if g.Key.Kind == protocol.ResourceFUEL {
							g.Amount = 0
							if err := state.Put(o, state.Key(state.KeyGrant, g.Key.Encode()), g); err != nil {
								return nil, err
							}
						}
					}
					return o.Changes(), nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := apply(txs[0]); err == nil {
					t.Fatal("warm cache ignored current grant")
				}
			}
		})
	}
}

func TestDirectCacheFixedBlockEquivalence(t *testing.T) {
	cfg, _, _, txs := cacheFixture(t, 63)
	var reference []state.Entry
	for _, mode := range []string{"disabled", "cold", "warm", "evicted", "restart"} {
		t.Run(mode, func(t *testing.T) {
			db := store.NewMemory()
			defer db.Close()
			e, err := NewEngine(cfg, db)
			if err != nil {
				t.Fatal(err)
			}
			e.directCache.disabled = mode == "disabled"
			if mode == "warm" || mode == "evicted" || mode == "restart" {
				for _, raw := range txs {
					if err := e.Check(raw); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "evicted" {
				for i := 0; i < verificationCacheEntries; i++ {
					e.directCache.put(protocol.Digest("evict", []byte{byte(i), byte(i >> 8)}), rules.VerifiedDirectPayment{}, 1)
				}
			}
			if mode == "restart" {
				e, err = NewEngine(cfg, db)
				if err != nil {
					t.Fatal(err)
				}
			}
			app, err := NewTimedApp("verify-cache", db, e.Check, e.ExecuteAt, e.BeginBlock)
			if err != nil {
				t.Fatal(err)
			}
			_, before, _, _, _, _ := e.directCache.stats()
			start := time.Now()
			prepared, err := app.PrepareProposal(context.Background(), &abci.RequestPrepareProposal{Height: 1, Txs: txs, MaxTxBytes: 1 << 30})
			if err != nil || len(prepared.Txs) != len(txs) {
				t.Fatal("prepare", err)
			}
			prepareDone := time.Now()
			processed, err := app.ProcessProposal(context.Background(), &abci.RequestProcessProposal{Height: 1, Txs: prepared.Txs})
			if err != nil || processed.Status != abci.ResponseProcessProposal_ACCEPT {
				t.Fatal("process", err)
			}
			processDone := time.Now()
			result, err := app.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{Height: 1, Hash: bytes.Repeat([]byte{1}, 32), Time: time.Unix(100, 0), Txs: prepared.Txs})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range result.TxResults {
				if r.Code != 0 {
					t.Fatal("execution rejected", r.Log)
				}
			}
			finalizeDone := time.Now()
			if _, err := app.Commit(context.Background(), &abci.RequestCommit{}); err != nil {
				t.Fatal(err)
			}
			entries := cacheLedger(t, db)
			if reference == nil {
				reference = entries
			} else if !reflect.DeepEqual(entries, reference) {
				t.Fatal("cache mode changed committed ledger")
			}
			_, after, _, _, _, _ := e.directCache.stats()
			want := uint64(len(txs))
			if mode == "disabled" {
				want *= 4
			}
			if mode == "warm" {
				want = 0
			}
			if after-before != want {
				t.Fatalf("full verifications=%d want=%d", after-before, want)
			}
			t.Logf("mode=%s txs=%d full=%d prepare_ms=%.3f process_ms=%.3f finalize_ms=%.3f", mode, len(txs), after-before, float64(prepareDone.Sub(start))/1e6, float64(processDone.Sub(prepareDone))/1e6, float64(finalizeDone.Sub(processDone))/1e6)
		})
	}
}

func TestDirectVerificationCacheReusesOnlyIdenticalBytes(t *testing.T) {
	cfg, _, _, txs := cacheFixture(t, 1)
	db := store.NewMemory()
	defer db.Close()
	e, err := NewEngine(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := e.Check(txs[0]); err != nil {
			t.Fatal(err)
		}
	}
	hits, misses, _, _, entries, _ := e.directCache.stats()
	if hits != 1 || misses != 1 || entries != 1 {
		t.Fatalf("identical payment was not reused: hits=%d full=%d entries=%d", hits, misses, entries)
	}
	for _, change := range []string{"owner", "funding", "opening", "qc"} {
		t.Run(change, func(t *testing.T) {
			p, err := protocol.DecodeDirectSubmission(txs[0])
			if err != nil {
				t.Fatal(err)
			}
			id := p.Tx.ID()
			var raw []byte
			switch change {
			case "owner":
				index := bytes.Index(txs[0], p.Tx.Auth[0].Signature[:])
				if index < 0 {
					t.Fatal("owner signature missing from wire bytes")
				}
				raw = bytes.Clone(txs[0])
				raw[index] ^= 1
				p.Tx.Auth[0].Signature[0] ^= 1
			case "funding":
				p.Tx.Funding[0].Ref[0] ^= 1
			case "opening":
				p.Tx.Funding[0].Opening[0] ^= 1
			case "qc":
				p.Authorization.Votes[0].Signature[0] ^= 1
			}
			if p.Tx.ID() != id {
				t.Fatal("test must preserve transaction identity")
			}
			if raw == nil {
				raw, err = p.MarshalBinary()
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := e.Check(raw); err == nil {
				t.Fatal("changed bytes reused successful authentication")
			}
		})
	}
	_, misses, _, _, entries, _ = e.directCache.stats()
	if misses != 5 || entries != 1 {
		t.Fatalf("invalid results cached: full=%d entries=%d", misses, entries)
	}
}

// Keep the complete wire parsing and all signature checks; bypass only reuse.
func BenchmarkDirectColdVerification(b *testing.B) {
	cfg, _, _, txs := cacheFixture(b, 32)
	db := store.NewMemory()
	defer db.Close()
	engine, err := NewEngine(cfg, db)
	if err != nil {
		b.Fatal(err)
	}
	engine.directCache.disabled = true
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := engine.Check(txs[i%len(txs)]); err != nil {
			b.Fatal(err)
		}
	}
}
