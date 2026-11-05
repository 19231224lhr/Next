package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/finality"
	"utxo/internal/budgetprobe"
	"utxo/internal/reservecontrol"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

func TestAutoCALRetriesIdenticalCommandUntilMemberQuorum(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	g := state.Grant{ID: protocol.Digest("grant", nil), Organization: protocol.Digest("org", nil), Key: protocol.ResourceKey{Kind: protocol.ResourceCAL, Account: protocol.Digest("account", nil), Version: 1}, Amount: 300}
	copy(g.Subject[:], key.Public().(ed25519.PublicKey))
	pub := reservecontrol.PublicState{Grant: 300}
	var members [4]reservecontrol.MemberState
	for i, a := range []uint64{100, 80, 40, 10} {
		members[i] = reservecontrol.MemberState{Valid: true, Grant: 300, Available: a}
	}
	var sent [][]byte
	runner := autoCALRunner{network: protocol.Digest("network", nil), grant: g, key: key, control: reservecontrol.Controller{Interval: time.Second, Lead: time.Second, Minimum: 100}, sample: func(context.Context) (reservecontrol.PublicState, [4]reservecontrol.MemberState, error) {
		return pub, members, nil
	}, submit: func(_ context.Context, raw []byte) error { sent = append(sent, bytes.Clone(raw)); return nil }}
	now := time.Unix(100, 0)
	if e := runner.tick(context.Background(), now); e != nil {
		t.Fatal(e)
	}
	if e := runner.tick(context.Background(), now.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if len(sent) != 2 || !bytes.Equal(sent[0], sent[1]) {
		t.Fatal("retry did not preserve signed command")
	}
	cmd, e := protocol.DecodeReserveIncrease(sent[0])
	if e != nil || cmd.Previous != 300 || cmd.Amount != 90 {
		t.Fatal(cmd, e)
	}
	pub.Grant = 390
	for i := 0; i < 3; i++ {
		members[i].Grant = 390
		members[i].Available += 60
	}
	if e := runner.tick(context.Background(), now.Add(2*time.Second)); e != nil {
		t.Fatal(e)
	}
	if len(sent) != 2 || runner.pending != nil {
		t.Fatal("confirmed top-up submitted again")
	}
}

func TestAutoCALPublicObserverAcceptsTopUpWithoutCallingItPayment(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	network := protocol.Digest("network", nil)
	c := protocol.ReserveIncrease{Network: network, Organization: protocol.Digest("org", nil), Grant: protocol.Digest("grant", nil), Key: protocol.ResourceKey{Kind: protocol.ResourceCAL, Account: protocol.Digest("account", nil), Version: 1}, Previous: 300, Amount: 90}
	c.Sign(key)
	raw, e := c.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	data, e := (protocol.ExecutionResult{}).MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	entries := []finality.ExecutedTx{{Bytes: raw, Data: data}}
	xs, e := c3DecodeExecutions(network, entries)
	if e != nil || len(xs) != 1 || xs[0].Kind != "reserve-increase" {
		t.Fatal("top-up stopped public observation", xs, e)
	}
	if _, e = c3DecodeExecutions(protocol.Digest("other", nil), entries); e == nil {
		t.Fatal("wrong network accepted")
	}
}

func TestAutoCALReadOnlySamplingToleratesOneUnavailableGM(t *testing.T) {
	org := protocol.OrgConfig{Org: protocol.Digest("org", nil)}
	g := state.Grant{Key: protocol.ResourceKey{Kind: protocol.ResourceCAL, Account: org.Org, Version: 1}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("sampling issued a write", r.Method)
		}
		if r.URL.Path == "/missing/debug/budget" {
			http.NotFound(w, r)
			return
		}
		s := budgetprobe.Snapshot{ApprovedCAL: 100, LargestCAL: 100, Resources: []budgetprobe.Resource{{Key: g.Key, Grant: 300, Slices: []state.Slice{{Available: 80}}, Usage: rules.PublicUsage{Reserved: 10, Spent: 20}}}}
		if r.URL.Path != "/debug/budget" && r.URL.Path != "/debug/budget/"+org.Org.String() {
			t.Error("incorrect sampling path", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(s)
	}))
	defer server.Close()
	n := cfg.Network{Members: map[protocol.Hash][4]string{org.Org: {server.URL, server.URL, server.URL, server.URL + "/missing"}}, CommitteeURLs: [4]string{server.URL}}
	p, ms, e := sampleAutoCAL(context.Background(), server.Client(), n, org, g)
	if e != nil || p.Grant != 300 || p.Reserved != 10 || p.Spent != 20 {
		t.Fatal(p, e)
	}
	for i := 0; i < 3; i++ {
		if !ms[i].Valid || ms[i].Available != 80 || ms[i].ApprovedCAL != 100 {
			t.Fatal(ms)
		}
	}
	if ms[3].Valid {
		t.Fatal("unavailable GM accepted")
	}
}

func TestAutoCALFundingPreparationDoesNotOverwriteExistingAccount(t *testing.T) {
	n := cfg.Network{Genesis: state.Genesis{Network: protocol.Digest("network", nil)}}
	g := state.Grant{Subject: protocol.PublicKey{1}}
	if e := prepareAutoCALFunding(&n, g, 1000); e != nil {
		t.Fatal(e)
	}
	if len(n.Accounts) != 1 || n.Accounts[0].Balance != 1000 {
		t.Fatal(n.Accounts)
	}
	if e := prepareAutoCALFunding(&n, g, 2000); e == nil || n.Accounts[0].Balance != 1000 {
		t.Fatal("funding account overwritten")
	}
}

func TestAutoCALPreservesWorkerSlices(t *testing.T) {
	g := state.Grant{Key: protocol.ResourceKey{Kind: protocol.ResourceCAL, Account: protocol.Digest("account", nil), Version: 1}}
	m, e := autoCALMemberState(budgetprobe.Snapshot{Resources: []budgetprobe.Resource{{Key: g.Key, Grant: 600, Slices: []state.Slice{{Available: 0}, {Available: 200}}}}}, g)
	if e != nil || len(m.Slices) != 2 || m.Slices[0] != 0 || m.Available != 200 {
		t.Fatal("worker capacity lost", m, e)
	}
}
