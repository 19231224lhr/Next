package sameorglab_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"utxo/internal/requesttrace"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type storeSpan struct {
	Role                            string
	Start, Callback, Evaluated, End int64
}

var profileEpoch = time.Now()

func profileNow() int64 { return time.Since(profileEpoch).Nanoseconds() }

type measuredStore struct {
	store.Store
	role  string
	mu    sync.Mutex
	spans []storeSpan
}

func (s *measuredStore) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	p := storeSpan{Role: s.role, Start: profileNow()}
	err := s.Store.Update(func(v state.ReadView) ([]state.Change, error) {
		p.Callback = profileNow()
		cs, e := fn(v)
		p.Evaluated = profileNow()
		return cs, e
	})
	p.End = profileNow()
	s.mu.Lock()
	s.spans = append(s.spans, p)
	s.mu.Unlock()
	return err
}

type paymentProfile struct {
	Index                   int
	Start, Collected, Ready int64
	Events                  []requesttrace.Event
	Stores                  []storeSpan
}
type profileRun struct {
	Name         string
	Round        int
	WorkloadHash string
	MemberURLs   []string
	Payments     []paymentProfile
	PrimitiveNS  map[string]int64
}

// Opt-in diagnostic only. NoSync applies only to fresh temporary test stores.
// No production durability or verification setting is changed.
func TestSameOrgProfile(t *testing.T) {
	path := os.Getenv("NEXT_SAME_ORG_PROFILE")
	if path == "" {
		t.Skip("opt-in stage profiling")
	}
	const n = 100
	var plan []protocol.FastTx
	t.Run("prepare", func(t *testing.T) {
		l := newLab(t, variants[0])
		var prev *protocol.DirectPayment
		for i := 0; i < n; i++ {
			r := l.request(t, i, prev)
			c, e := l.f.DirectCertificate(r.Tx, l.policy)
			must(t, e)
			plan = append(plan, r.Tx)
			prev = &protocol.DirectPayment{Tx: r.Tx, Certificate: c, InputCertificates: r.InputCertificates}
		}
	})
	configs := []struct {
		name                                     string
		cache, memberNoSync, walletNoSync, trace bool
	}{
		{"durable", false, false, false, true},
		{"cache-durable", true, false, false, true},
		{"member-nosync", false, true, false, true},
		{"wallet-nosync", false, false, true, true},
		{"both-nosync", false, true, true, true},
		{"durable-no-trace", false, false, false, false},
	}
	var runs []profileRun
	for round := 0; round < 3; round++ {
		for offset := range configs {
			cfg := configs[(offset+round)%len(configs)]
			t.Run(fmt.Sprintf("r%d-%s", round+1, cfg.name), func(t *testing.T) {
				var stores []*measuredStore
				l := newLabWithStore(t, variant{name: cfg.name, cache: cfg.cache}, func(role, path string, id store.Identity) (store.Store, error) {
					var db store.Store
					var e error
					if (strings.HasPrefix(role, "member") && cfg.memberNoSync) || (strings.HasPrefix(role, "wallet") && cfg.walletNoSync) {
						db, e = store.OpenNoSync(path, id)
					} else {
						db, e = store.Open(path, id)
					}
					if e != nil {
						return nil, e
					}
					s := &measuredStore{Store: db, role: role}
					stores = append(stores, s)
					return s, nil
				})
				run := profileRun{Name: cfg.name, Round: round + 1, PrimitiveNS: map[string]int64{}}
				for _, c := range l.clients {
					run.MemberURLs = append(run.MemberURLs, c.BaseURL)
				}
				var previous *protocol.DirectPayment
				var ids []byte
				for i := 0; i < n; i++ {
					req := protocol.DirectRequest{Tx: plan[i]}
					if previous != nil {
						req.InputCertificates = []protocol.InputCertificate{{Certificate: previous.Certificate, Index: 0}}
					}
					ctx := context.Background()
					if cfg.trace {
						ctx = requesttrace.Start(ctx, "gateway")
					}
					p := paymentProfile{Index: i, Start: profileNow()}
					cert, e := l.collector.CollectDirect(ctx, req)
					must(t, e)
					p.Collected = profileNow()
					must(t, l.wallets[1-i%2].ReceiveDirect(req.Tx.Body.Outputs[0], cert, 0))
					p.Ready = profileNow()
					p.Events = requesttrace.Events(ctx)
					for _, s := range stores {
						s.mu.Lock()
						for _, sp := range s.spans {
							if sp.Start >= p.Start && sp.End <= p.Ready {
								p.Stores = append(p.Stores, sp)
							}
						}
						s.mu.Unlock()
					}
					run.Payments = append(run.Payments, p)
					previous = &protocol.DirectPayment{Tx: req.Tx, Certificate: cert, InputCertificates: req.InputCertificates}
					l.install(t, *previous)
					id := req.Tx.ID()
					ids = append(ids, id[:]...)
				}
				run.WorkloadHash = protocol.Digest("SAME_ORG_PROFILE", ids).String()
				// Separate serial primitive costs; not added to parallel end-to-end time.
				for _, which := range []string{"parent-certificate", "initial-commitment", "wallet-certificate-output"} {
					start := time.Now()
					for j := 0; j < 100; j++ {
						switch which {
						case "parent-certificate":
							must(t, previous.Certificate.Verify(l.f.Org))
						case "initial-commitment":
							must(t, previous.Tx.VerifyInitial(l.policy.Key))
						case "wallet-certificate-output":
							must(t, previous.Certificate.VerifyOutput(l.f.Org, 0, previous.Tx.Body.Outputs[0]))
						}
					}
					run.PrimitiveNS[which] = time.Since(start).Nanoseconds() / 100
				}
				runs = append(runs, run)
			})
		}
	}
	raw, e := json.MarshalIndent(runs, "", "  ")
	must(t, e)
	must(t, os.MkdirAll(filepath.Dir(path), 0755))
	must(t, os.WriteFile(path, raw, 0644))
}
