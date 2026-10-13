package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/finality"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/wallet"
	"utxo/protocol"
)

func TestC3FeesRefundNetAccounting(t *testing.T) {
	h := c3FeePayment{FeeInput: state.OriginOutput{Output: protocol.Output{Amount: 10000}}, FeeChange: wallet.DirectCoin{Output: protocol.Output{Amount: 9990}}}
	net, e := c3ObservedFeeNet(h)
	if e != nil || net != 10 {
		t.Fatalf("absent refund: %d %v", net, e)
	}
	h.FeeRefund = &wallet.DirectCoin{Output: protocol.Output{Amount: 3}}
	net, e = c3ObservedFeeNet(h)
	if e == nil || net != 7 {
		t.Fatalf("separate refund must reduce measured net and fail C3 assertion: %d %v", net, e)
	}
}

func TestC3FeesSubmissionAttempts(t *testing.T) {
	var rows []c3SubmitAttempt
	calls := 0
	start := time.Now()
	submit := func(context.Context, []byte) error {
		calls++
		if calls < 3 {
			return errors.New("committee submission HTTP 503")
		}
		return nil
	}
	for i := 0; i < 3; i++ {
		_ = c3SubmitRecorded(context.Background(), start, []byte{1}, submit, &rows)
	}
	if len(rows) != 3 || rows[0].Error == "" || rows[1].Error == "" || rows[2].Error != "" || !rows[2].Accepted || rows[0].Accepted {
		t.Fatalf("lost retry outcomes: %+v", rows)
	}
	for _, r := range rows {
		if r.EndOffsetNS < r.StartOffsetNS {
			t.Fatal("negative attempt duration")
		}
	}
}

func TestC3FeesReplayExactEvidence(t *testing.T) {
	raw := []byte{1, 2, 3}
	blocks := []c3FeeBlock{{Height: 5, Transactions: []finality.ExecutedTx{{Bytes: raw}}}, {Height: 6, Transactions: []finality.ExecutedTx{{Bytes: []byte{9}}}}}
	if c3FindReplay(blocks, raw, 5) != nil {
		t.Fatal("unrelated newer block is not replay evidence")
	}
	blocks = append(blocks, c3FeeBlock{Height: 7, Transactions: []finality.ExecutedTx{{Bytes: []byte{9}}, {Bytes: raw, Code: 12, Data: []byte{4}}}})
	got := c3FindReplay(blocks, raw, 5)
	if got == nil || got.Height != 7 || got.Index != 1 || got.Code != 12 {
		t.Fatalf("wrong inclusion: %+v", got)
	}
}

func TestC3FeesReplayHTTPOutcome(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "duplicate", 503) }))
	out, e := c3ReplaySubmit(context.Background(), s.Client(), s.URL, []byte{1})
	if e != nil || out.Outcome != "http-rejected" || out.HTTPStatus != 503 || out.ResponseBody == "" {
		t.Fatalf("HTTP rejection missing: %+v %v", out, e)
	}
	s.Close()
	out, e = c3ReplaySubmit(context.Background(), s.Client(), s.URL, []byte{1})
	if e == nil || out.Outcome != "transport-inconclusive" || out.HTTPStatus != 0 {
		t.Fatalf("transport failure misclassified: %+v %v", out, e)
	}
}

func TestC3FeesFixtureAndSignedQuote(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lab")
	if e := initialize([]string{"--dir", dir, "--c3", "--outputs", "2"}); e != nil {
		t.Fatal(e)
	}
	if e := c3FeesCommand([]string{"--dir", dir, "--prepare", "--count", "2", "--timeout", "2s"}); e != nil {
		t.Fatal(e)
	}
	var lab cfg.Lab
	var n cfg.Network
	if e := cfg.Read(filepath.Join(dir, "lab.json"), &lab); e != nil {
		t.Fatal(e)
	}
	if e := cfg.Read(lab.Network, &n); e != nil {
		t.Fatal(e)
	}
	if n.Direct.TimeoutSeconds != 2 {
		t.Fatal("timeout not frozen")
	}
	var fixture c3FeesFixture
	if e := cfg.Read(filepath.Join(dir, "c3-fees-fixture.json"), &fixture); e != nil {
		t.Fatal(e)
	}
	if len(fixture.Requests) != 2 {
		t.Fatal("signed transaction identities must be frozen in fixture")
	}
	for _, pair := range fixture.Requests {
		p, e := n.Direct.Policy(n.Schedule, n.Organizations)
		if e != nil {
			t.Fatal(e)
		}
		v, e := rules.PrepareDirectVector(pair.Source.Tx, p)
		if e != nil {
			t.Fatal(e)
		}
		s := protocol.SummaryFor(pair.Source.Tx, v)
		if pair.Child.Tx.Body.Inputs[0].Output != s.OutputID(0) || pair.Child.Tx.Body.Inputs[0].Evidence != protocol.Hash(s.Fact()) {
			t.Fatal("child does not bind frozen source")
		}
		if pair.Source.Tx.VerifyAuth() != nil || pair.Child.Tx.VerifyAuth() != nil {
			t.Fatal("fixture signature invalid")
		}
	}
	counts := map[protocol.PublicKey]int{}
	ids := map[protocol.OutputID]bool{}
	for _, o := range n.Genesis.Outputs {
		if o.Output.Asset == protocol.AssetFUEL {
			counts[o.Output.Recipient.Owner]++
			if ids[o.ID] {
				t.Fatal("reused fee input")
			}
			ids[o.ID] = true
		}
	}
	for _, k := range lab.Owners {
		key, e := cfg.PrivateKey(k)
		if e != nil {
			t.Fatal(e)
		}
		var owner protocol.PublicKey
		copy(owner[:], key[32:])
		if counts[owner] != 2 {
			t.Fatalf("fee count %d", counts[owner])
		}
	}
	for _, issuer := range n.Direct.C3.Issuers {
		found := false
		for _, g := range n.Genesis.Grants {
			if g.ID == issuer.Funding.Grant {
				found = true
			}
		}
		if !found {
			t.Fatal("removed issuer pool grant")
		}
	}
	p, e := n.Direct.Policy(n.Schedule, n.Organizations)
	if e != nil {
		t.Fatal(e)
	}
	key, e := cfg.PrivateKey(lab.Owners[0])
	if e != nil {
		t.Fatal(e)
	}
	var cal, fee state.OriginOutput
	for _, o := range n.Genesis.Outputs {
		if string(o.Output.Recipient.Owner[:]) != string(key[32:]) {
			continue
		}
		if o.Output.Asset == protocol.AssetCAL {
			cal = o
		} else if o.Output.Asset == protocol.AssetFUEL {
			fee = o
		}
	}
	r, e := c3FeesRequest(n, p, n.Organizations[0], key, protocol.Input{Kind: protocol.FinalInput, Output: cal.ID, Evidence: cal.Fact}, cal.Output, cal.Output.Recipient, nil, fee)
	if e != nil {
		t.Fatal(e)
	}
	if r.Tx.Body.Fee.Maximum != 10 || r.Tx.Body.Fee.C3.Quote != 10 || r.Tx.Body.Fee.C3.Budget != 6 {
		t.Fatalf("unexpected fees: %+v", r.Tx.Body.Fee)
	}
	if _, e = rules.PrepareDirectVector(r.Tx, p); e != nil {
		t.Fatal(e)
	}
	if r.Tx.Auth[0] != protocol.SignOwner(r.Tx.ID(), key) {
		t.Fatal("quote changed after signing")
	}
	s, e := rules.NewC3Escrow(n.Genesis.Network, r.Tx.Body.Config, protocol.SpendFactID(protocol.Digest("test")), r.Tx.Body.Fee.C3)
	if e != nil {
		t.Fatal(e)
	}
	if e = c3CheckRecord(rules.C3Record{Escrow: s}); e != nil {
		t.Fatal(e)
	}
	s.Held--
	if c3CheckRecord(rules.C3Record{Escrow: s}) == nil {
		t.Fatal("accepted missing budget")
	}
}

func TestC3FeesVerifiedExecutionDecoder(t *testing.T) {
	net := protocol.Digest("network")
	d := protocol.CompensationDecision{Network: net, Output: protocol.OutputID(protocol.Digest("source")), Height: 7}
	raw, e := d.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	result, e := (protocol.CompensationResult{Decision: d.ID(), Applied: false}).MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	entries := []finality.ExecutedTx{{Bytes: raw, Code: 1}, {Bytes: raw, Data: result}}
	got, e := c3DecodeExecutions(net, entries)
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != 1 || got[0].Index != 1 || got[0].Kind != "compensation" || got[0].Decision.Output != d.Output || got[0].Result.Applied {
		t.Fatalf("incorrect committed execution attribution: %+v", got)
	}
	if _, e = c3DecodeExecutions(protocol.Digest("different-network"), entries); e == nil {
		t.Fatal("accepted wrong network")
	}
	entries[1].Data = []byte("invalid")
	if _, e = c3DecodeExecutions(net, entries); e == nil {
		t.Fatal("accepted corrupt result")
	}
}

func TestC3FeesRejectsInvalidOptionsBeforeReadingLab(t *testing.T) {
	for _, args := range [][]string{{"--dir", "absent", "--count", "0"}, {"--dir", "absent", "--case", "invented"}, {"--dir", "absent", "--source-delay", "-1s"}} {
		if c3FeesCommand(args) == nil {
			t.Fatal(args)
		}
	}
}
