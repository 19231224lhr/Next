package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
	"utxo/finality"
	"utxo/internal/committee"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

type retryPublic struct {
	attempts [][]byte
	proofs   map[protocol.Hash]finality.FactProof
}

func (p *retryPublic) Submit(_ context.Context, b []byte) error {
	p.attempts = append(p.attempts, bytes.Clone(b))
	return nil
}
func (p *retryPublic) Certificate(context.Context, protocol.SpendFactID) (protocol.TXCer, error) {
	return protocol.TXCer{}, state.ErrNotFound
}
func (p *retryPublic) Receipt(_ context.Context, _ protocol.FactKind, k protocol.Hash) (finality.FactProof, error) {
	if f, ok := p.proofs[k]; ok {
		return f, nil
	}
	return finality.FactProof{}, state.ErrNotFound
}

type stalledProof struct{ retryPublic }

func (p *stalledProof) Receipt(ctx context.Context, _ protocol.FactKind, _ protocol.Hash) (finality.FactProof, error) {
	<-ctx.Done()
	return finality.FactProof{}, ctx.Err()
}

func TestUnavailableProofEndpointDoesNotPreventSubmission(t *testing.T) {
	f := testkit.NewFixture("stalled-proof", "a", 1)
	c, err := f.Certify(f.Transaction(0, 1))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := c.MarshalBinary()
	key := state.Key(state.KeyOutbox, c.QC.Fact[:])
	db := store.NewMemory()
	defer db.Close()
	p := state.Outbox{Fact: c.QC.Fact, Certificate: raw, Origin: f.Org.Org}
	db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		e := state.Put(o, key, p)
		return o.Changes(), e
	})
	public := new(stalledProof)
	r := Relay{DB: db, Public: public, Organizations: map[protocol.Hash]protocol.OrgConfig{f.Org.Hash(): f.Org}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = r.deliver(ctx, key, p)
	if len(public.attempts) != 1 || ctx.Err() != nil {
		t.Fatal("proof polling consumed the delivery deadline")
	}
}

func TestRelayRetrySurvivesRestartAndCacheWithoutProgress(t *testing.T) {
	f := testkit.NewFixture("retry", "a", 1)
	c, err := f.Certify(f.Transaction(0, 1))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := c.MarshalBinary()
	key := state.Key(state.KeyOutbox, c.QC.Fact[:])
	id := store.Identity{Network: f.Org.Network.String(), Role: "wallet", Node: "retry", Schema: 2}
	path := filepath.Join(t.TempDir(), "wallet.db")
	db, err := store.Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	public := &retryPublic{}
	relay := Relay{DB: db, Public: public, Organizations: map[protocol.Hash]protocol.OrgConfig{f.Org.Hash(): f.Org}}
	pending := state.Outbox{Fact: c.QC.Fact, Certificate: raw, Origin: f.Org.Org}
	err = db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		e := state.Put(o, key, pending)
		return o.Changes(), e
	})
	if err != nil {
		t.Fatal(err)
	}
	deliver := func() {
		t.Helper()
		var p state.Outbox
		err := relay.DB.View(func(v state.ReadView) error { var e error; p, _, e = state.Load[state.Outbox](v, key); return e })
		if err != nil {
			t.Fatal(err)
		}
		_ = relay.deliver(context.Background(), key, p)
	}
	expire := func(generation bool) {
		t.Helper()
		err := relay.DB.Update(func(v state.ReadView) ([]state.Change, error) {
			b, e := v.Get(key)
			if e != nil {
				return nil, e
			}
			var p map[string]json.RawMessage
			if e = json.Unmarshal(b, &p); e != nil {
				return nil, e
			}
			p["NextSubmitUnixNS"] = json.RawMessage("0")
			if generation {
				p["AttemptStartedUnixNS"], _ = json.Marshal(time.Now().Add(-time.Minute).UnixNano())
			}
			b, e = json.Marshal(p)
			return []state.Change{{Key: key, Value: b}}, e
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	deliver()
	deliver()
	if len(public.attempts) != 1 {
		t.Fatalf("proof polling resubmitted %d times", len(public.attempts))
	}
	first, err := protocol.DecodeSubmission(public.attempts[0])
	if err != nil {
		t.Fatal("missing stable submission", err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	relay.DB = db
	// A 202 receipt may be lost with the receiving node. The persisted task survives.
	deliver()
	if len(public.attempts) != 1 {
		t.Fatal("restart ignored retry schedule")
	}
	expire(false)
	deliver()
	if len(public.attempts) != 2 || !bytes.Equal(public.attempts[0], public.attempts[1]) {
		t.Fatal("ordinary retry changed envelope")
	}
	// Repeated 202/cache hints cannot indefinitely renew the generation deadline.
	expire(true)
	deliver()
	if len(public.attempts) != 3 || bytes.Equal(public.attempts[1], public.attempts[2]) {
		t.Fatal("cache-only no-progress attempt never replaced")
	}
	last, err := protocol.DecodeSubmission(public.attempts[2])
	if err != nil || !bytes.Equal(first.Body, last.Body) || first.Network != last.Network {
		t.Fatal("recovery changed payment identity", err)
	}
}

func TestRelayRequiresVerifiedWorkBeforeStoppingSubmission(t *testing.T) {
	f := testkit.NewFixture("work-retry", "a", 1)
	c, err := f.Certify(f.Transaction(0, 1))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := c.MarshalBinary()
	publicDB := store.NewMemory()
	defer publicDB.Close()
	engine, err := committee.NewEngine(committee.EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Accounts: []committee.GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1_000_000_000}}}, publicDB)
	if err != nil {
		t.Fatal(err)
	}
	var transition state.Transition
	err = publicDB.View(func(v state.ReadView) error { var e error; transition, e = engine.Execute(v, raw); return e })
	if err != nil {
		t.Fatal(err)
	}
	proofs := make(map[protocol.Hash]finality.FactProof)
	var trust finality.Trust
	var missing protocol.Hash
	for _, fact := range transition.Facts {
		var proof finality.FactProof
		trust, proof, err = testkit.Proof("work-retry", fact)
		if err != nil {
			t.Fatal(err)
		}
		proofs[fact.Key] = proof
		if fact.Kind == protocol.FactCustody {
			missing = fact.Key
		}
	}
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{false: "forged", true: "verified"}[valid], func(t *testing.T) {
			db := store.NewMemory()
			defer db.Close()
			key := state.Key(state.KeyOutbox, c.QC.Fact[:])
			p := state.Outbox{Fact: c.QC.Fact, Certificate: raw, Origin: f.Org.Org}
			db.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				e := state.Put(o, key, p)
				return o.Changes(), e
			})
			client := &retryPublic{proofs: make(map[protocol.Hash]finality.FactProof)}
			for k, proof := range proofs {
				if k != missing {
					if !valid {
						proof.Fact.Revision++
					}
					client.proofs[k] = proof
				}
			}
			r := Relay{DB: db, Public: client, Trust: trust, Organizations: map[protocol.Hash]protocol.OrgConfig{f.Org.Hash(): f.Org}}
			_ = r.deliver(context.Background(), key, p)
			var stored state.Outbox
			db.View(func(v state.ReadView) error { stored, _, err = state.Load[state.Outbox](v, key); return err })
			if err != nil {
				t.Fatal(err)
			}
			if stored.PublicComplete != valid {
				t.Fatal("completion flag not bound to verified terminal work")
			}
			stored.NextSubmitUnixNS = 0
			stored.AttemptStartedUnixNS = time.Now().Add(-time.Minute).UnixNano()
			db.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				e := state.Put(o, key, stored)
				return o.Changes(), e
			})
			_ = r.deliver(context.Background(), key, stored)
			if valid && len(client.attempts) != 0 {
				t.Fatal("completed work resubmitted while custody proof missing")
			}
			if !valid && len(client.attempts) != 2 {
				t.Fatal("forged proof stopped retries")
			}
			// Supplying the missing authenticated proofs permits normal queue retirement.
			client.proofs = proofs
			if err = r.deliver(context.Background(), key, stored); err != nil {
				t.Fatal(err)
			}
			if err = db.View(func(v state.ReadView) error { _, e := v.Get(key); return e }); err != state.ErrNotFound {
				t.Fatal("queue did not drain")
			}
		})
	}
}
