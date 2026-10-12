package sameorglab_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/protocol"
)

type tamperApprovalTransport struct {
	base http.RoundTripper
	kind string
}

func (p tamperApprovalTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, e := p.base.RoundTrip(r)
	if e != nil {
		return response, e
	}
	raw, e := io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil {
		return nil, e
	}
	if response.StatusCode == 200 {
		switch p.kind {
		case "signature":
			raw[len(raw)-1] ^= 1
		case "allocation":
			raw[54] ^= 1
		case "member":
			raw[len(raw)-65] = 9
		case "trailing":
			raw = append(raw, 0)
		}
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	response.ContentLength = int64(len(raw))
	return response, nil
}
func TestCompactResponseDoesNotTrustReconstructedFields(t *testing.T) {
	for _, kind := range []string{"signature", "allocation", "member", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			l := newLabWithStore(t, variant{compactReply: true}, func(string, string, store.Identity) (store.Store, error) { return store.NewMemory(), nil })
			req := l.request(t, 0, nil)
			for i := 0; i < 2; i++ {
				l.clients[i].HTTP.Transport = tamperApprovalTransport{base: l.clients[i].HTTP.Transport, kind: kind}
			}
			if _, e := l.collector.CollectDirect(context.Background(), req); e == nil {
				t.Fatal("two invalid responses formed a quorum")
			}
			for i := 0; i < 2; i++ {
				l.clients[i].HTTP.Transport = l.clients[i].HTTP.Transport.(tamperApprovalTransport).base
			}
			cert, e := l.collector.CollectDirect(context.Background(), req)
			must(t, e)
			must(t, cert.Verify(l.f.Org))
		})
	}
}

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("test member unavailable")
}
func TestContinuationDoesNotPinParentQuorum(t *testing.T) {
	l := newLabWithStore(t, variant{continuation: true}, func(string, string, store.Identity) (store.Store, error) { return store.NewMemory(), nil })
	first := l.request(t, 0, nil)
	cert := protocol.OutputCertificate{}
	for i := 0; i < 3; i++ {
		a, e := l.members[i].ApproveDirect(context.Background(), first)
		must(t, e)
		cert.Summary = a.Summary
		cert.QC.Fact = a.Summary.Fact()
		cert.QC.Votes = append(cert.QC.Votes, a.Vote)
	}
	parent := protocol.DirectPayment{Tx: first.Tx, Certificate: cert}
	child := l.request(t, 1, &parent)
	l.clients[0].HTTP.Transport = failingRoundTripper{}
	got, e := l.collector.CollectDirect(context.Background(), child)
	must(t, e)
	must(t, got.Verify(l.f.Org))
	var seenFourth bool
	for _, v := range got.QC.Votes {
		if v.Member == 3 {
			seenFourth = true
		}
		if v.Member == 0 {
			t.Fatal("offline member returned a vote")
		}
	}
	if !seenFourth {
		t.Fatal("new quorum could not include fourth member")
	}
	if l.misses.Load() != 0 {
		t.Fatal("non-parent signer was probed with a compact request")
	}
}

func TestContinuationMultipleOutputsOfSameParent(t *testing.T) {
	l := newLabWithStore(t, variant{continuation: true, compactReply: true}, func(string, string, store.Identity) (store.Store, error) { return store.NewMemory(), nil })
	original := l.request(t, 0, nil)
	body := original.Tx.Body
	body.Outputs = []protocol.Output{{Asset: protocol.AssetCAL, Amount: 40, Recipient: l.desc[1]}, {Asset: protocol.AssetCAL, Amount: 60, Recipient: l.desc[1]}}
	tx, e := protocol.NewFastTx(body, original.Tx.Claims, l.policy.Key)
	must(t, e)
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), l.keys[0])}
	req := protocol.DirectRequest{Tx: tx}
	cert, e := l.collector.CollectDirect(context.Background(), req)
	must(t, e)
	parent := protocol.DirectPayment{Tx: tx, Certificate: cert}
	child := l.request(t, 1, &parent)
	body = child.Tx.Body
	body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: cert.Summary.OutputID(0), Evidence: protocol.Hash(cert.QC.Fact)}, {Kind: protocol.CertificateInput, Output: cert.Summary.OutputID(1), Evidence: protocol.Hash(cert.QC.Fact)}}
	claims := []protocol.InputClaim{{Output: parent.Tx.Body.Outputs[0]}, {Output: parent.Tx.Body.Outputs[1]}}
	if bytes.Compare(body.Inputs[0].Output[:], body.Inputs[1].Output[:]) > 0 {
		body.Inputs[0], body.Inputs[1] = body.Inputs[1], body.Inputs[0]
		claims[0], claims[1] = claims[1], claims[0]
	}
	tx, e = protocol.NewFastTx(body, claims, l.policy.Key)
	must(t, e)
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), l.keys[1])}
	child = protocol.DirectRequest{Tx: tx, InputCertificates: []protocol.InputCertificate{{Certificate: cert, Index: 0}, {Certificate: cert, Index: 1}}}
	got, e := l.collector.CollectDirect(context.Background(), child)
	must(t, e)
	must(t, got.Verify(l.f.Org))
	var results result
	l.replay(t, []protocol.DirectPayment{{Tx: tx, Certificate: got, InputCertificates: child.InputCertificates}, parent}, &results)
	if results.CALFinal != 0 || results.GapFinal != 0 {
		t.Fatal("multi-input settlement did not close")
	}
}

func TestContinuationHTTPFallbackAndPublicCompatibility(t *testing.T) {
	l := newLabWithStore(t, variant{continuation: true}, func(string, string, store.Identity) (store.Store, error) { return store.NewMemory(), nil })
	first := l.request(t, 0, nil)
	cert := protocol.OutputCertificate{}
	for i := 0; i < 3; i++ {
		a, e := l.members[i].ApproveDirect(context.Background(), first)
		must(t, e)
		cert.Summary = a.Summary
		cert.QC.Fact = a.Summary.Fact()
		cert.QC.Votes = append(cert.QC.Votes, a.Vote)
	}
	parent := protocol.DirectPayment{Tx: first.Tx, Certificate: cert}
	child := l.request(t, 1, &parent)
	full, compact, signers, e := child.MarshalForContinuation()
	must(t, e)
	if signers != 7 {
		t.Fatal(signers)
	}
	// Member 3 never approved the parent, so compact decoding must request the full witness.
	_, e = l.clients[3].ApproveDirectContinuation(context.Background(), compact, full)
	must(t, e)
	if l.requests.Load() != 2 || l.misses.Load() != 1 {
		t.Fatalf("fallback requests=%d misses=%d", l.requests.Load(), l.misses.Load())
	}
	bad, _, e := protocol.ContinueDirectRequest(child)
	must(t, e)
	bad.Parents[0].QC.Votes[0].Signature[0] ^= 1
	wire, e := bad.MarshalBinary()
	must(t, e)
	before := l.requests.Load()
	if _, e = l.clients[0].ApproveDirectContinuation(context.Background(), wire, full); e == nil {
		t.Fatal("forged QC fell back to valid full request")
	}
	if l.requests.Load() != before+1 {
		t.Fatal("authentication failure retried")
	}
	// An old member endpoint remains interoperable.
	var oldFull, oldCompact int
	handler := transport.MemberHandler(l.members[0], 4, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/transactions/continuation" {
			oldCompact++
			http.NotFound(w, r)
			return
		}
		oldFull++
		handler.ServeHTTP(w, r)
	}))
	defer srv.Close()
	_, e = transport.NewMemberClient(srv.URL).ApproveDirectContinuation(context.Background(), compact, full)
	must(t, e)
	if oldCompact != 1 || oldFull != 1 {
		t.Fatal("old peer fallback failed")
	}
	// Standard output certificates and complete submissions still execute unchanged.
	c, e := l.collector.CollectDirect(context.Background(), child)
	must(t, e)
	payments := []protocol.DirectPayment{parent, {Tx: child.Tx, Certificate: c, InputCertificates: child.InputCertificates}}
	previous := &payments[1]
	for i := 2; i < 10; i++ {
		req := l.request(t, i, previous)
		c, e := l.collector.CollectDirect(context.Background(), req)
		must(t, e)
		payments = append(payments, protocol.DirectPayment{Tx: req.Tx, Certificate: c, InputCertificates: req.InputCertificates})
		previous = &payments[len(payments)-1]
	}
	for i, j := 0, len(payments)-1; i < j; i, j = i+1, j-1 {
		payments[i], payments[j] = payments[j], payments[i]
	}
	var results result
	l.replay(t, payments, &results)
	if results.CALFinal != 0 || results.GapFinal != 0 {
		t.Fatalf("reversed public execution did not close: %+v", results)
	}
}

func TestContinuationRejectsInvalidEvidence(t *testing.T) {
	l := newLabWithStore(t, variant{continuation: true}, func(string, string, store.Identity) (store.Store, error) { return store.NewMemory(), nil })
	first := l.request(t, 0, nil)
	cert := protocol.OutputCertificate{}
	for i := 0; i < 3; i++ {
		a, e := l.members[i].ApproveDirect(context.Background(), first)
		must(t, e)
		cert.Summary = a.Summary
		cert.QC.Fact = a.Summary.Fact()
		cert.QC.Votes = append(cert.QC.Votes, a.Vote)
	}
	parent := protocol.DirectPayment{Tx: first.Tx, Certificate: cert}
	child := l.request(t, 1, &parent)
	c, use, e := protocol.ContinueDirectRequest(child)
	must(t, e)
	if !use || c.Signers() != 7 {
		t.Fatal("incorrect eligibility/signers")
	}
	raw, e := c.MarshalBinary()
	must(t, e)
	if _, e = l.members[3].ApproveDirectBytes(context.Background(), raw); !errors.Is(e, member.ErrParentApprovalMissing) {
		t.Fatalf("missing state = %v", e)
	}
	for _, kind := range []string{"signature", "duplicate-vote", "index", "truncated", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			bad, _, e := protocol.ContinueDirectRequest(child)
			must(t, e)
			switch kind {
			case "signature":
				bad.Parents[0].QC.Votes[1].Signature[0] ^= 1
			case "duplicate-vote":
				bad.Parents[0].QC.Votes[1] = bad.Parents[0].QC.Votes[0]
			case "index":
				bad.Parents[0].Index = 10000
			}
			wire, e := bad.MarshalBinary()
			must(t, e)
			if kind == "truncated" {
				wire = wire[:len(wire)-1]
			}
			if kind == "trailing" {
				wire = append(wire, 0)
			}
			before := l.quota(t, protocol.ResourceCAL)
			if _, e = l.members[0].ApproveDirectBytes(context.Background(), wire); e == nil {
				t.Fatal("invalid evidence accepted")
			}
			if l.quota(t, protocol.ResourceCAL) != before {
				t.Fatal("rejected request changed quota")
			}
		})
	}
	// Re-instantiation discards the summary cache; retained approval still suffices.
	cfg := member.Config{Organization: l.f.Org, Index: 0, Key: l.f.Keys[0], Peers: []protocol.OrgConfig{l.f.Org}, Schedule: l.f.Schedule, Workers: 1, Direct: &l.settings, DirectContinuationEntries: 1}
	restarted, e := member.New(cfg, l.dbs[0], l.f.Genesis)
	must(t, e)
	a, e := restarted.ApproveDirectBytes(context.Background(), raw)
	must(t, e)
	before := l.quota(t, protocol.ResourceCAL)
	again, e := restarted.ApproveDirectBytes(context.Background(), raw)
	must(t, e)
	if a.Vote != again.Vote || l.quota(t, protocol.ResourceCAL) != before {
		t.Fatal("retry was not idempotent")
	}
	conflict := l.request(t, 3, &parent)
	cc, _, e := protocol.ContinueDirectRequest(conflict)
	must(t, e)
	wire, e := cc.MarshalBinary()
	must(t, e)
	if _, e = restarted.ApproveDirectBytes(context.Background(), wire); !errors.Is(e, rules.ErrConflict) {
		t.Fatalf("double spend = %v", e)
	}
	// The member without the parent approval can use the complete certificate.
	_, e = l.members[3].ApproveDirect(context.Background(), child)
	must(t, e)
}

func TestContinuationWithoutInstall(t *testing.T) {
	l := newLabWithStore(t, variants[0], func(string, string, store.Identity) (store.Store, error) { return store.NewMemory(), nil })
	first := l.request(t, 0, nil)
	cert, err := l.collector.CollectDirect(context.Background(), first)
	must(t, err)
	parent := protocol.DirectPayment{Tx: first.Tx, Certificate: cert}
	child := l.request(t, 1, &parent)
	compact, use, err := protocol.ContinueDirectRequest(child)
	must(t, err)
	if !use {
		t.Fatal("same-group child was not eligible")
	}
	raw, err := compact.MarshalBinary()
	must(t, err)
	full, err := child.MarshalBinary()
	must(t, err)
	if len(raw) >= len(full) {
		t.Fatal("compact request is not smaller")
	}
	for _, vote := range cert.QC.Votes {
		m := l.members[vote.Member]
		a, err := m.ApproveDirectBytes(context.Background(), raw)
		must(t, err)
		must(t, l.dbs[vote.Member].View(func(v state.ReadView) error {
			fact := a.Summary.Fact()
			saved, found, e := state.Load[state.Approval](v, state.Key(state.KeyApproval, fact[:]))
			if e != nil {
				return e
			}
			if !found || len(saved.DirectParents) != 1 {
				t.Fatal("full parent evidence not retained")
			}
			return saved.DirectParents[0].Certificate.Verify(l.f.Org)
		}))
	}
	// A known parent fact never authenticates altered quorum signatures.
	compact.Parents[0].QC.Votes[0].Signature[0] ^= 1
	bad, err := compact.MarshalBinary()
	must(t, err)
	if _, err = l.members[cert.QC.Votes[0].Member].ApproveDirectBytes(context.Background(), bad); err == nil {
		t.Fatal("forged parent QC accepted")
	}
}
