package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/finality"
	"utxo/internal/requesttrace"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type sample struct {
	BackendTrace                                    []requesttrace.SettlementTiming `json:",omitempty"`
	CommitObservedMicros                            int64                           `json:",omitempty"`
	SettlementHeight                                int64
	ProofHeaderHeight                               int64
	Trace                                           []requesttrace.Event `json:",omitempty"`
	Hop                                             int
	Spend                                           string
	FastMicros, WalletReadyMicros, FinalProofMicros int64
}

func demo(args []string) error {
	flags := flag.NewFlagSet("demo", flag.ContinueOnError)
	dir := flags.String("dir", "", "laboratory directory")
	observeBlock := flags.Bool("observe-block", false, "poll committed state before final proof (single hop only)")
	traceEnabled := flags.Bool("trace", false, "include diagnostic stage timestamps (same-host comparison only)")
	hops := flags.Int("hops", 8, "alternating cross-organization transfers")
	input := flags.Int("input", 0, "unused genesis output index for owner zero")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if *observeBlock && *hops != 1 {
		return fmt.Errorf("observe-block requires one hop")
	}
	if *hops < 1 || *hops > 32 {
		return fmt.Errorf("demo supports 1..32 hops; sustained workloads use bench")
	}
	var lab labConfig
	if e := cfg.Read(filepath.Join(*dir, "lab.json"), &lab); e != nil {
		return e
	}
	var network cfg.Network
	if e := cfg.Read(lab.Network, &network); e != nil {
		return e
	}
	trust, e := network.Trust()
	if e != nil {
		return e
	}
	// Do not reserve wallet inputs until the launcher's foreground endpoints exist.
	readiness := &http.Client{Timeout: time.Second}
	for _, url := range lab.Gateways {
		ready := false
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			response, err := readiness.Get(url + "/healthz")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					ready = true
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !ready {
			return fmt.Errorf("gateway unavailable: %s", url)
		}
	}
	var keys [2]ed25519.PrivateKey
	var wallets [2]*wallet.Wallet
	for i, path := range lab.Owners {
		keys[i], e = cfg.PrivateKey(path)
		if e != nil {
			return e
		}
		db, e := store.Open(filepath.Join(*dir, fmt.Sprintf("wallet%d.db", i)), store.Identity{Network: network.Genesis.Network.String(), Role: "wallet", Node: fmt.Sprint(i), Schema: 2})
		if e != nil {
			return e
		}
		defer db.Close()
		stopRelay, err := network.StartWalletRelay(db)
		if err != nil {
			return err
		}
		defer stopRelay()
		var owner protocol.PublicKey
		copy(owner[:], keys[i][32:])
		wallets[i], e = wallet.New(db, network.Genesis.Network, owner, network.Organizations)
		if e != nil {
			return e
		}
	}
	var origins []protocol.Input
	var amount uint64
	for _, out := range network.Genesis.Outputs {
		if bytes.Equal(out.Output.Recipient.Owner[:], keys[0][32:]) {
			origins = append(origins, protocol.Input{Kind: protocol.FinalInput, Output: out.ID, Evidence: out.Fact})
			amount = out.Output.Amount
		}
	}
	if *input < 0 || *input >= len(origins) {
		return fmt.Errorf("invalid input index")
	}
	current := origins[*input]
	var parent *protocol.TXCer
	client := &http.Client{Timeout: 15 * time.Second}
	public := transport.NewCommitteeClient(network.CommitteeURLs[0])
	var certificates []protocol.TXCer
	var samples []sample
	var starts []time.Time
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for hop := 0; hop < *hops; hop++ {
		sender := hop % 2
		recipient := 1 - sender
		org := network.Organizations[sender]
		descriptor := protocol.NewDescriptor(network.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: network.Organizations[recipient].Org}, keys[recipient])
		var subject protocol.PublicKey
		copy(subject[:], keys[sender][32:])
		b := protocol.TxBody{Wire: 2, Version: 2, Network: network.Genesis.Network, Kind: protocol.FastTransfer, Subject: subject, Certifier: org.Org, Epoch: org.Epoch, Config: org.Hash(), Rules: network.Schedule.IDs(), Inputs: []protocol.Input{current}, Outputs: []protocol.Output{{Asset: protocol.AssetCAL, Amount: amount, Recipient: descriptor}}, Fee: protocol.FeeTerms{Source: protocol.OrgReserve, Account: org.Org, Version: 1, Maximum: 100}, Work: protocol.WorkLimit{Execution: 10000, Bytes: 1 << 20, Depth: 64, Ancestors: 256}}
		if _, e = rand.Read(b.Nonce[:]); e != nil {
			return e
		}
		for _, g := range network.Genesis.Grants {
			if g.Organization == org.Hash() {
				b.Admission = append(b.Admission, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
				if g.Key.Kind == protocol.ResourcePolicy {
					b.Fee.Policy = g.Key.Account
				}
			}
		}
		b.Intent = b.IntentID()
		signed := protocol.SignedTx{Body: b, Auth: []protocol.OwnerAuth{protocol.SignOwner(b.ID(), keys[sender])}}
		request := protocol.PaymentRequest{Tx: signed}
		if parent != nil {
			request.Parents = []protocol.TXCer{*parent}
		}
		if e = wallets[sender].SaveRequest(request); e != nil {
			return e
		}
		raw, e := request.MarshalBinary()
		if e != nil {
			return e
		}
		httpRequest, e := http.NewRequestWithContext(ctx, http.MethodPost, lab.Gateways[sender]+"/v1/transactions", bytes.NewReader(raw))
		if e != nil {
			return e
		}
		httpRequest.Header.Set("Content-Type", transport.MediaType)
		traceCtx := ctx
		if *traceEnabled {
			traceCtx = requesttrace.Start(ctx, "wallet")
			httpRequest.Header.Set(requesttrace.HeaderName, "1")
		}
		start := time.Now() // Wallet HTTP submission, after durable preparation and encoding.
		requesttrace.Mark(traceCtx, "http_submit")
		response, e := client.Do(httpRequest)
		if e != nil {
			return e
		}
		requesttrace.Mark(traceCtx, "response_headers_received")
		requesttrace.Import(traceCtx, response.Header.Get(requesttrace.HeaderName), "")
		raw, e = io.ReadAll(io.LimitReader(response.Body, protocol.MaxCertificateBytes+1))
		response.Body.Close()
		if e != nil {
			return e
		}
		if response.StatusCode != 200 {
			return fmt.Errorf("hop %d: HTTP %d: %s", hop, response.StatusCode, raw)
		}
		requesttrace.Mark(traceCtx, "response_body_received")
		c, e := protocol.DecodeCertificate(raw)
		if e != nil {
			return e
		}
		if e = c.Verify(org); e != nil {
			return e
		}
		fast := time.Since(start).Microseconds()
		requesttrace.Mark(traceCtx, "certificate_verified")
		if e = wallets[recipient].Receive(c, 0); e != nil {
			return e
		}
		ready := time.Since(start).Microseconds()
		requesttrace.Mark(traceCtx, "recipient_persisted")
		samples = append(samples, sample{Trace: requesttrace.Events(traceCtx), Hop: hop, Spend: protocol.Hash(c.QC.Fact).String(), FastMicros: fast, WalletReadyMicros: ready})
		starts = append(starts, start)
		certificates = append(certificates, c)
		parent = &certificates[len(certificates)-1]
		current = protocol.Input{Kind: protocol.CertificateInput, Output: c.Effects.Outputs[0], Evidence: protocol.Hash(c.QC.Fact)}
	}
	for i, c := range certificates {
		if *observeBlock {
			if err := observeCommit(ctx, client, network.CommitteeURLs[0]+"/v1/outcomes/"+protocol.Hash(c.QC.Fact).String()); err != nil {
				return err
			}
			samples[i].CommitObservedMicros = time.Since(starts[i]).Microseconds()
		}
		key := protocol.Hash(c.Effects.Outputs[0])
		verified := false
		for ctx.Err() == nil {
			proof, e := public.Receipt(ctx, protocol.FactOutputCreated, key)
			if e == nil {
				fact, e := finality.Verify(trust, proof)
				if e != nil {
					return e
				}
				output, e := protocol.DecodeSettledOutput(fact.Fact().Payload)
				if e != nil || output.ID != c.Effects.Outputs[0] {
					return fmt.Errorf("unexpected settled output")
				}
				samples[i].FinalProofMicros = time.Since(starts[i]).Microseconds()
				samples[i].SettlementHeight = proof.Commitment.Height
				samples[i].ProofHeaderHeight = proof.Header.Height
				verified = true
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(100 * time.Millisecond):
			}
		}
		if verified && *observeBlock && requesttrace.Settlement != nil {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, network.CommitteeURLs[0]+"/debug/settlement/"+protocol.Hash(c.QC.Fact).String(), nil)
			if err != nil {
				return err
			}
			response, err := client.Do(req)
			if err != nil {
				return err
			}
			if response.StatusCode == http.StatusOK {
				err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&samples[i].BackendTrace)
			} else {
				err = fmt.Errorf("backend trace HTTP %d", response.StatusCode)
			}
			response.Body.Close()
			if err != nil {
				return err
			}
		}
		if !verified {
			return fmt.Errorf("hop %d final proof not available: %w", i, ctx.Err())
		}
	}
	report := struct {
		Network   string
		Processes int
		Scope     string
		Samples   []sample
	}{network.ChainID, len(lab.Nodes), "Timing origin wallet_http_submit_v2: immediately before HTTP Client.Do; wallet-ready ends after recipient verification and synchronous persistence. Excludes sender preparation, signing, persistence and encoding; includes HTTP transport waiting and connection setup. Sequential cross-organization functional run, synchronous bbolt; final-proof times include polling after the fast chain. CommitObservedMicros, when requested, is the first SETTLED observation from committee 0 (10ms polling plus query delay), not an exact consensus timestamp or an independently verified proof. SettlementHeight and ProofHeaderHeight are obtained from the subsequently verified output proof. Not a sustained TPS benchmark.", samples}
	path := filepath.Join(*dir, "reports", fmt.Sprintf("demo-%d.json", time.Now().UnixNano()))
	if e = cfg.Write(path, report); e != nil {
		return e
	}
	output, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(output))
	fmt.Println("Report:", path)
	return nil
}
