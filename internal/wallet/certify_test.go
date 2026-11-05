package wallet

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/internal/transport"
	"utxo/protocol"
)

type directApproverFunc func(context.Context, protocol.DirectRequest) (protocol.DirectApproval, error)

func (f directApproverFunc) ApproveDirect(c context.Context, r protocol.DirectRequest) (protocol.DirectApproval, error) {
	return f(c, r)
}

func walletApprovalFixture(t *testing.T) (testkit.Fixture, protocol.DirectRequest, [4]DirectApprover, [4]*member.Member) {
	t.Helper()
	f := testkit.NewFixture("wallet-direct-qc", "org", 1)
	f.EnableDirect()
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	settings := rules.DirectSettings{RequireApprovalDeadline: true, Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	p, err := settings.Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.FastTransaction(0, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	body := tx.Body
	body.ApprovalDeadline = time.Now().Unix() + protocol.ApprovalLifetimeSeconds
	tx, err = protocol.NewFastTx(body, tx.Claims, p.Key)
	if err != nil {
		t.Fatal(err)
	}
	tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), f.Owner)}
	var clients [4]DirectApprover
	var members [4]*member.Member
	for i := range members {
		db := store.NewMemory()
		t.Cleanup(func() { db.Close() })
		members[i], err = member.New(member.Config{Organization: f.Org, Index: uint16(i), Key: f.Keys[i], Peers: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Workers: 1, Direct: &settings}, db, f.Genesis)
		if err != nil {
			t.Fatal(err)
		}
		clients[i] = members[i]
	}
	return f, protocol.DirectRequest{Tx: tx}, clients, members
}

func TestWalletCollectsQCWithoutCollector(t *testing.T) {
	f, req, clients, members := walletApprovalFixture(t)
	stopped := make(chan struct{})
	clients[3] = directApproverFunc(func(ctx context.Context, _ protocol.DirectRequest) (protocol.DirectApproval, error) {
		<-ctx.Done()
		close(stopped)
		return protocol.DirectApproval{}, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := CollectDirect(ctx, f.Org, clients, req)
	if err != nil || c.Verify(f.Org) != nil || len(c.QC.Votes) != 3 || c.Summary.ApprovalDeadline != req.Tx.Body.ApprovalDeadline {
		t.Fatal("wallet did not assemble the signed certificate", err)
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("slow fourth GM was not cancelled")
	}
	before, err := members[0].Quota(f.Genesis.Grants[0].Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	clients[3] = directApproverFunc(func(context.Context, protocol.DirectRequest) (protocol.DirectApproval, error) {
		return protocol.DirectApproval{}, errors.New("offline")
	})
	if _, err = CollectDirect(ctx, f.Org, clients, req); err != nil {
		t.Fatal(err)
	}
	after, err := members[0].Quota(f.Genesis.Grants[0].Key, 0)
	if err != nil || before != after {
		t.Fatal("retry reserved quota again", err)
	}
	db := store.NewMemory()
	defer db.Close()
	w, err := NewWithGenesis(db, f.Org.Network, req.Tx.Body.Outputs[0].Recipient.Owner, []protocol.OrgConfig{f.Org}, f.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.ReceiveDirect(req.Tx.Body.Outputs[0], c, 0); err != nil {
		t.Fatal("receiver could not accept without collector", err)
	}
}

func TestWalletRejectsWrongDeadlineAndDuplicateSigner(t *testing.T) {
	for _, name := range []string{"deadline", "duplicate", "signature"} {
		t.Run(name, func(t *testing.T) {
			f, req, clients, members := walletApprovalFixture(t)
			clients[2] = directApproverFunc(func(ctx context.Context, r protocol.DirectRequest) (protocol.DirectApproval, error) {
				a, err := members[2].ApproveDirect(ctx, r)
				if err != nil {
					return a, err
				}
				switch name {
				case "deadline":
					a.Summary.ApprovalDeadline++
					a.Vote = protocol.SignSpend(a.Summary.Fact(), 2, f.Keys[2])
				case "duplicate":
					a.Vote.Member = 1
				case "signature":
					a.Vote.Signature[0] ^= 1
				}
				return a, nil
			})
			clients[3] = directApproverFunc(func(context.Context, protocol.DirectRequest) (protocol.DirectApproval, error) {
				return protocol.DirectApproval{}, errors.New("offline")
			})
			if _, err := CollectDirect(context.Background(), f.Org, clients, req); err == nil {
				t.Fatal("invalid quorum accepted")
			}
		})
	}
}

func TestWalletDirectHTTPAndBackgroundFeed(t *testing.T) {
	f, req, _, members := walletApprovalFixture(t)
	var clients [4]DirectApprover
	var peers [4]*transport.MemberClient
	for i, m := range members {
		server := httptest.NewServer(transport.MemberHandler(m, 4, 4))
		t.Cleanup(server.Close)
		peers[i] = transport.NewMemberClient(server.URL)
		clients[i] = peers[i]
	}
	c, err := CollectDirect(context.Background(), f.Org, clients, req)
	if err != nil || c.Verify(f.Org) != nil {
		t.Fatal(err)
	}
	for _, vote := range c.QC.Votes {
		page, err := peers[vote.Member].ApprovedDirect(context.Background(), nil)
		if err != nil || len(page.Items) != 1 || page.Items[0].Request.Tx.ID() != req.Tx.ID() {
			t.Fatal("collector cannot retrieve committed material", err)
		}
		resp, err := peers[vote.Member].HTTP.Get(peers[vote.Member].BaseURL + "/v3/approved?after=invalid")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatal("invalid feed cursor accepted")
		}
	}
}
