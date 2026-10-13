package main

import (
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/committee"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func c3CLIFixture(t *testing.T) (string, cfg.Lab, cfg.Network, testkit.Fixture) {
	t.Helper()
	f := testkit.NewFixture("c3-cli", "org", 1)
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
	n := cfg.Network{ChainID: "c3-cli", Genesis: f.Genesis, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Direct: &rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}, Accounts: []committee.GenesisAccount{{Owner: f.Org.Org, Asset: protocol.AssetCAL, Balance: 1000000000}, {Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1000000000}}}
	if err = configureC3Lab(&n); err != nil {
		t.Fatal(err)
	}
	trust, _ := testkit.Block(n.ChainID, 1, nil, nil, nil)
	for i, v := range trust.Validators.Validators {
		copy(n.Committee[i][:], v.PubKey.Bytes())
	}
	dir := t.TempDir()
	if err = os.Mkdir(filepath.Join(dir, "reports"), 0700); err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(dir, "owner.key")
	if err = os.WriteFile(owner, f.Owner, 0600); err != nil {
		t.Fatal(err)
	}
	lab := cfg.Lab{Network: filepath.Join(dir, "network.json"), Owners: [2]string{owner, owner}}
	return dir, lab, n, f
}

func TestC3CLIPreparationPreservesFundedIssuer(t *testing.T) {
	for _, mode := range []string{"fault", "liability"} {
		t.Run(mode, func(t *testing.T) {
			_, lab, n, f := c3CLIFixture(t)
			issuer := n.Direct.C3.Issuers[0]
			if mode == "fault" {
				if err := prepareFault(lab, n, 1); err != nil {
					t.Fatal(err)
				}
				if err := cfg.Read(lab.Network, &n); err != nil {
					t.Fatal(err)
				}
			} else {
				d := f.Genesis.Outputs[0].Output.Recipient
				n = liabilityGenesis(n, [3]protocol.ReceiveDescriptor{d, d, d})
			}
			var grant, balance uint64
			for _, g := range n.Genesis.Grants {
				if g.ID == issuer.Funding.Grant {
					grant = g.Amount
				}
			}
			for _, a := range n.Accounts {
				if a.Owner == issuer.Funding.Key.Account && a.Asset == protocol.AssetFUEL {
					balance = a.Balance
				}
			}
			if grant != 1000000000000 || balance != grant {
				t.Fatalf("issuer backing lost: grant=%d balance=%d", grant, balance)
			}
			db := store.NewMemory()
			defer db.Close()
			if _, err := committee.NewEngine(n.Engine(), db); err != nil {
				t.Fatal("prepared genesis cannot boot", err)
			}
			requests, err := faultRequests(lab, n, 1)
			if err != nil {
				t.Fatal(err)
			}
			p, err := n.Direct.Policy(n.Schedule, n.Organizations)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = rules.PrepareDirectVector(requests[0].Tx, p); err != nil {
				t.Fatal(err)
			}
			if !requests[0].Tx.Body.Fee.C3.Enabled() {
				t.Fatal("missing C3 commitment")
			}
		})
	}
}

func TestC3FaultLoadSavesRequestAndReceivesCertificate(t *testing.T) {
	dir, lab, n, f := c3CLIFixture(t)
	// Build owner FUEL outputs without using prepareFault: this regression isolates
	// wallet policy propagation from the separate genesis-preservation regression.
	d := f.Genesis.Outputs[0].Output.Recipient
	n.Genesis.Outputs = append(n.Genesis.Outputs, state.OriginOutput{ID: protocol.OutputID(protocol.Digest("c3-cli-fuel")), Fact: protocol.Digest("c3-cli-fuel-final"), Output: protocol.Output{Asset: protocol.AssetFUEL, Amount: 10000, Recipient: d}})
	p, err := n.Direct.Policy(n.Schedule, n.Organizations)
	if err != nil {
		t.Fatal(err)
	}
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer unavailable.Close()
	for i := range n.CommitteeURLs {
		n.CommitteeURLs[i] = unavailable.URL
	}
	n.Members = map[protocol.Hash][4]string{f.Org.Org: {unavailable.URL, unavailable.URL, unavailable.URL, unavailable.URL}}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
			w.WriteHeader(500)
			return
		}
		req, e := protocol.DecodeDirectRequest(raw)
		if e != nil {
			t.Error(e)
			w.WriteHeader(500)
			return
		}
		cert, e := f.DirectCertificate(req.Tx, p)
		if e != nil {
			t.Error(e)
			w.WriteHeader(500)
			return
		}
		raw, e = cert.MarshalBinary()
		if e != nil {
			t.Error(e)
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write(raw)
	}))
	defer gateway.Close()
	lab.Gateways[0] = gateway.URL
	err = runFaultLoad(dir, lab, n, 1, 1, 0, time.Second)
	// No finality service is running; successful fast receipt must precede this
	// expected incomplete-observation result rather than fail authentication.
	if err == nil || !strings.Contains(err.Error(), "E4 incomplete") {
		t.Fatalf("load stopped before observation: %v", err)
	}
	var report faultReport
	if err = cfg.Read(filepath.Join(dir, "reports", "fault-v4.json"), &report); err != nil {
		t.Fatal(err)
	}
	s := report.Samples[0]
	if s.Error != "" || s.ReadyNS == 0 || s.Certificate == nil {
		t.Fatalf("C3 fast receipt failed: %+v", s)
	}
	if err = s.Certificate.Verify(f.Org); err != nil {
		t.Fatal(err)
	}
}
