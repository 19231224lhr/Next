package recipient

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
	"utxo/internal/gateway"
	"utxo/internal/poollab"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/transport"
	"utxo/protocol"
)

func request(t *testing.T, l *poollab.Lab, previous *protocol.OutputCertificate, owner, to int) protocol.DirectRequest {
	t.Helper()
	tx, e := l.OrdinaryTemplate(owner, 1)
	if e != nil {
		t.Fatal(e)
	}
	b := tx.Body
	b.Subject = l.Descriptors[owner].Owner
	input := l.Fixture.Genesis.Outputs[0].Output
	var parents []protocol.InputCertificate
	if previous != nil {
		input = protocol.Output{Asset: protocol.AssetCAL, Amount: 100, Recipient: l.Descriptors[owner]}
		b.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: previous.Summary.OutputID(0), Evidence: protocol.Hash(previous.QC.Fact)}}
		parents = []protocol.InputCertificate{{Certificate: *previous, Index: 0}}
	}
	b.Outputs = []protocol.Output{{Asset: protocol.AssetCAL, Amount: 100, Recipient: l.Descriptors[to]}}
	tx, e = protocol.NewFastTx(b, []protocol.InputClaim{{Output: input}}, l.Policy.Key)
	if e != nil {
		t.Fatal(e)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), l.Keys[owner])}
	return protocol.DirectRequest{Tx: tx, InputCertificates: parents}
}

type alteredMember struct {
	gateway.MemberClient
	direct gateway.DirectMemberClient
	kind   string
	calls  *atomic.Int32
}

func (m alteredMember) ApproveDirect(ctx context.Context, r protocol.DirectRequest) (protocol.DirectApproval, error) {
	if m.calls != nil {
		m.calls.Add(1)
	}
	a, e := m.direct.ApproveDirect(ctx, r)
	if e != nil {
		return a, e
	}
	switch m.kind {
	case "bad-signature":
		a.Vote.Signature[0] ^= 1
	case "mixed-fact":
		a.Summary.Tx[0] ^= 1
	case "duplicate":
		a.Vote.Member = 0
	}
	return a, nil
}
func (m alteredMember) InstallDirect(ctx context.Context, p protocol.DirectPayment) error {
	return m.direct.InstallDirect(ctx, p)
}

func TestReceiverCollectsAndRetainsFullPayment(t *testing.T) {
	l, e := poollab.New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	path := filepath.Join(t.TempDir(), "collector.db")
	identity := store.Identity{Network: "receiver-test", Role: "collector", Node: "1", Schema: 1}
	db, e := store.Open(path, identity)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { db.Close() }()
	var clients [4]gateway.MemberClient
	for i, s := range l.Servers {
		clients[i] = transport.NewMemberClient(s.URL)
	}
	c, e := gateway.New(l.Fixture.Org, clients, db)
	if e != nil {
		t.Fatal(e)
	}
	h, e := New(c, l.Wallets[1], l.Descriptors[1].Owner, l.Fixture.Org, db)
	if e != nil {
		t.Fatal(e)
	}
	req := request(t, l, nil, 0, 1)
	raw, e := req.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/collect", bytes.NewReader(raw))
	r.Header.Set("Content-Type", transport.MediaType)
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("collect: %d %s", w.Code, w.Body.String())
	}
	cert, e := protocol.DecodeOutputCertificate(w.Body.Bytes())
	if e != nil {
		t.Fatal(e)
	}
	child := request(t, l, &cert, 1, 2)
	childRaw, e := child.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	prepared := httptest.NewRecorder()
	prep := httptest.NewRequest(http.MethodPost, "/prepare", bytes.NewReader(childRaw))
	prep.Header.Set("Content-Type", transport.MediaType)
	h.PrepareHTTP(prepared, prep)
	if prepared.Code != http.StatusNoContent {
		t.Fatalf("received output not spendable: %d %s", prepared.Code, prepared.Body.String())
	}
	// Reopen the durable collector store before recovering the full payment.
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = store.Open(path, identity)
	if e != nil {
		t.Fatal(e)
	}
	c, e = gateway.New(l.Fixture.Org, clients, db)
	if e != nil {
		t.Fatal(e)
	}
	reopened, e := New(c, l.Wallets[1], l.Descriptors[1].Owner, l.Fixture.Org, db)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := reopened.Payment(req.Tx.ID())
	if e != nil || saved.Certificate.QC.Fact != cert.QC.Fact {
		t.Fatal("full payment recovery", e)
	}
	if e = db.View(func(v state.ReadView) error { _, e := v.Get(state.Key(state.KeyOutbox, cert.QC.Fact[:])); return e }); e != nil {
		t.Fatal("public relay material missing", e)
	}
	// Start a fresh relay without a volatile inbox or wake notification. It must
	// submit the retained payment from the reopened store alone.
	submitted := make(chan []byte, 4)
	trust, _ := testkit.Block("receiver-restart", 1, nil, nil, nil)
	relay := &gateway.Relay{DB: db, Direct: true, Trust: trust, Public: recoveringPublic{submitted: submitted},
		Organizations: map[protocol.Hash]protocol.OrgConfig{l.Fixture.Org.Hash(): l.Fixture.Org},
		Members:       map[protocol.Hash][4]gateway.MemberClient{l.Fixture.Org.Org: clients}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case got := <-submitted:
		want, err := saved.Submission().MarshalBinary()
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("restart relay changed retained payment", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("restart relay did not submit durable payment")
	}
}

type recoveringPublic struct {
	gateway.PublicClient
	submitted chan []byte
}

func (p recoveringPublic) Submit(_ context.Context, raw []byte) error {
	p.submitted <- bytes.Clone(raw)
	return nil
}

func TestReceiverRejectsWrongOwnerAndBadQuorums(t *testing.T) {
	for _, kind := range []string{"wrong-owner", "bad-signature", "mixed-fact", "duplicate", "disk-failure"} {
		t.Run(kind, func(t *testing.T) {
			l, e := poollab.New("")
			if e != nil {
				t.Fatal(e)
			}
			defer l.Close()
			var db store.Store = store.NewMemory()
			defer db.Close()
			if kind == "disk-failure" {
				db = failedStore{db}
			}
			var calls atomic.Int32
			var clients [4]gateway.MemberClient
			for i, s := range l.Servers {
				m := transport.NewMemberClient(s.URL)
				clients[i] = alteredMember{m, m, kind, &calls}
			}
			c, e := gateway.New(l.Fixture.Org, clients, db)
			if e != nil {
				t.Fatal(e)
			}
			h, e := New(c, l.Wallets[1], l.Descriptors[1].Owner, l.Fixture.Org, db)
			if e != nil {
				t.Fatal(e)
			}
			to := 1
			if kind == "wrong-owner" {
				to = 2
			}
			req := request(t, l, nil, 0, to)
			raw, _ := req.MarshalBinary()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/collect", bytes.NewReader(raw))
			r.Header.Set("Content-Type", transport.MediaType)
			h.ServeHTTP(w, r)
			if kind == "wrong-owner" && calls.Load() != 0 {
				t.Fatal("wrong recipient contacted members")
			}
			if w.Code == 200 {
				t.Fatal("accepted invalid recipient/quorum")
			}
			if _, e = h.Payment(req.Tx.ID()); e == nil {
				t.Fatal("persisted invalid result")
			}
		})
	}
}

type failedStore struct{ store.Store }

func (f failedStore) Update(func(state.ReadView) ([]state.Change, error)) error {
	return errors.New("injected disk failure")
}
