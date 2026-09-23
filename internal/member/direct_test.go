package member_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestDirectDurableApprovalAndBackgroundInstall(t *testing.T) {
	f := testkit.NewFixture("direct-member", "org", 1)
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
	tx, err := f.FastTransaction(0, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := f.FastTransaction(0, 2, p)
	if err != nil {
		t.Fatal(err)
	}
	cert := protocol.OutputCertificate{}
	members := make([]*member.Member, 3)
	var dbs []store.Store
	var paths []string
	var configs []member.Config
	defer func() {
		for _, db := range dbs {
			db.Close()
		}
	}()
	for i := 0; i < 3; i++ {
		path := filepath.Join(t.TempDir(), "member.db")
		identity := store.Identity{Network: "direct-member", Role: "member", Node: "node", Schema: 3}
		db, err := store.Open(path, identity)
		if err != nil {
			t.Fatal(err)
		}
		cfg := member.Config{Organization: f.Org, Index: uint16(i), Key: f.Keys[i], Peers: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Workers: 1, Direct: &settings}
		m, err := member.New(cfg, db, f.Genesis)
		if err != nil {
			t.Fatal(err)
		}
		byteApprover, ok := any(m).(interface {
			ApproveDirectBytes(context.Context, []byte) (protocol.DirectApproval, error)
		})
		if !ok {
			t.Fatal("member has no single-decode HTTP entry point")
		}
		encoded, err := (protocol.DirectRequest{Tx: tx}).MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		bad := bytes.Clone(encoded)
		sig := bytes.Index(bad, tx.Auth[0].Signature[:])
		if sig < 0 {
			t.Fatal("signature absent in encoding")
		}
		bad[sig] ^= 1
		if _, err := byteApprover.ApproveDirectBytes(context.Background(), bad); err == nil {
			t.Fatal("invalid signature accepted by byte entry point")
		}
		if _, err := byteApprover.ApproveDirectBytes(context.Background(), encoded[:len(encoded)-1]); err == nil {
			t.Fatal("truncated request accepted")
		}
		vote, err := byteApprover.ApproveDirectBytes(context.Background(), encoded)
		if err != nil {
			t.Fatal(err)
		}
		cert.Summary = vote.Summary
		cert.QC.Fact = vote.Summary.Fact()
		cert.QC.Votes = append(cert.QC.Votes, vote.Vote)
		quota, err := m.Quota(f.Genesis.Grants[0].Key, 0)
		if err != nil || quota.Reserved != 100 {
			t.Fatalf("CAL not reserved: %+v %v", quota, err)
		}
		if _, err = m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: conflict}); err == nil {
			t.Fatal("conflicting vote issued")
		}
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
		db, err = store.Open(path, identity)
		if err != nil {
			t.Fatal(err)
		}
		dbs = append(dbs, db)
		m, err = member.New(cfg, db, f.Genesis)
		if err != nil {
			t.Fatal(err)
		}
		members[i] = m
		paths = append(paths, path)
		configs = append(configs, cfg)
		if _, err = m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: conflict}); err == nil {
			t.Fatal("restart forgot input lock")
		}
		again, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx})
		if err != nil || again.Vote != vote.Vote {
			t.Fatal("durable vote not replayable")
		}
	}
	if err = cert.Verify(f.Org); err != nil {
		t.Fatal(err)
	}
	body := tx.Body
	body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: cert.Summary.OutputID(0), Evidence: protocol.Hash(cert.QC.Fact)}}
	body.Nonce[0] = 3
	body.Intent = body.IntentID()
	child, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: tx.Body.Outputs[0]}}, p.Key)
	if err != nil {
		t.Fatal(err)
	}
	child.Auth = []protocol.OwnerAuth{protocol.SignOwner(child.ID(), f.Owner)}
	req := protocol.DirectRequest{Tx: child, InputCertificates: []protocol.InputCertificate{{Certificate: cert, Index: 0}}}
	for i, m := range members {
		if _, err = m.ApproveDirect(context.Background(), req); err != nil {
			t.Fatalf("successor waited for INSTALL: %v", err)
		}
		if err = m.InstallDirect(protocol.DirectPayment{Tx: tx, Certificate: cert}); err != nil {
			t.Fatal(err)
		}
		stored, err := m.InstallDirectClassified(protocol.DirectPayment{Tx: tx, Certificate: cert})
		if err != nil || !stored {
			t.Fatalf("real INSTALL persistence was not reported: stored=%v err=%v", stored, err)
		}
		if err = dbs[i].Update(func(v state.ReadView) ([]state.Change, error) {
			o := state.NewOverlay(v)
			if err := state.Put(o, state.Key(state.KeyObserved, cert.QC.Fact[:]), true); err != nil {
				return nil, err
			}
			return o.Changes(), nil
		}); err != nil {
			t.Fatal(err)
		}
		stored, err = m.InstallDirectClassified(protocol.DirectPayment{Tx: tx, Certificate: cert})
		if err != nil || stored {
			t.Fatalf("already observed payment counted as an INSTALL copy: stored=%v err=%v", stored, err)
		}
		pending := func() state.Outbox {
			t.Helper()
			var value state.Outbox
			err := dbs[i].View(func(v state.ReadView) error {
				var found bool
				var err error
				value, found, err = state.Load[state.Outbox](v, state.Key(state.KeyOutbox, cert.QC.Fact[:]))
				if !found {
					t.Error("installed outbox missing")
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			return value
		}
		first := pending().NextSubmitUnixNS
		if first <= time.Now().UnixNano() {
			t.Fatal("member did not defer its first fallback")
		}
		if err := dbs[i].Close(); err != nil {
			t.Fatal(err)
		}
		dbs[i], err = store.Open(paths[i], store.Identity{Network: "direct-member", Role: "member", Node: "node", Schema: 3})
		if err != nil {
			t.Fatal(err)
		}
		restarted, err := member.New(configs[i], dbs[i], f.Genesis)
		if err != nil {
			t.Fatal(err)
		}
		if err := restarted.InstallDirect(protocol.DirectPayment{Tx: tx, Certificate: cert}); err != nil {
			t.Fatal(err)
		}
		if pending().NextSubmitUnixNS != first {
			t.Fatal("restart or repeated INSTALL extended fallback grace period")
		}

	}
}
