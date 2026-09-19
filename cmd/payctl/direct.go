package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/blockfollow"
	"utxo/internal/gateway"
	"utxo/internal/rules"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

func demoDirect(args []string) error {
	flags := flag.NewFlagSet("demo-v3", flag.ContinueOnError)
	dir := flags.String("dir", "", "laboratory directory")
	index := flags.Int("input", 0, "genesis output index per owner")
	withhold := flags.Bool("withhold-parent", false, "certify parent but withhold its submission to exercise compensation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	var lab cfg.Lab
	if err := cfg.Read(filepath.Join(*dir, "lab.json"), &lab); err != nil {
		return err
	}
	var n cfg.Network
	if err := cfg.Read(lab.Network, &n); err != nil {
		return err
	}
	if n.Direct == nil || *index < 0 {
		return protocol.ErrRule
	}
	p, err := n.Direct.Policy(n.Schedule, n.Organizations)
	if err != nil {
		return err
	}
	trust, err := n.Trust()
	if err != nil {
		return err
	}
	owner0, err := cfg.PrivateKey(lab.Owners[0])
	if err != nil {
		return err
	}
	owner1, err := cfg.PrivateKey(lab.Owners[1])
	if err != nil {
		return err
	}
	desc0 := protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: n.Organizations[0].Org}, owner0)
	desc1 := protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: n.Organizations[1].Org}, owner1)
	outputs := n.Genesis.Outputs
	selected := -1
	count := 0
	for i, out := range outputs {
		if out.Output.Recipient.Owner == desc0.Owner {
			if count == *index {
				selected = i
				break
			}
			count++
		}
	}
	if selected < 0 {
		return protocol.ErrRule
	}
	origin := outputs[selected]
	build := func(org protocol.OrgConfig, input protocol.Input, claim protocol.Output, to protocol.ReceiveDescriptor, nonce byte) (protocol.FastTx, error) {
		body := protocol.TxBody{Wire: 4, Version: 4, Network: n.Genesis.Network, Kind: protocol.FastTransfer, Subject: claim.Recipient.Owner, Certifier: org.Org, Config: org.Hash(), Epoch: org.Epoch, Rules: p.Rules(), Inputs: []protocol.Input{input}, Outputs: []protocol.Output{{Asset: protocol.AssetCAL, Amount: claim.Amount, Recipient: to}}, Fee: protocol.FeeTerms{Source: protocol.OrgReserve, Account: org.Org, Version: 1, Maximum: 1000}, Work: protocol.WorkLimit{Execution: 100000, Bytes: 10000000, Depth: 1, Ancestors: 1}}
		for _, g := range n.Genesis.Grants {
			if g.Organization == org.Hash() {
				body.Admission = append(body.Admission, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
				if g.Key.Kind == protocol.ResourcePolicy {
					body.Fee.Policy = g.Key.Account
				}
			}
		}
		unique := protocol.Digest("DIRECT_DEMO_NONCE", input.Output[:], []byte{nonce})
		copy(body.Nonce[:], unique[:])
		body.Intent = body.IntentID()
		tx, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: claim}}, p.Key)
		if err != nil {
			return tx, err
		}
		key := owner0
		if claim.Recipient.Owner == desc1.Owner {
			key = owner1
		}
		tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), key)}
		return tx, nil
	}
	db0, err := store.Open(filepath.Join(*dir, fmt.Sprintf("wallet-v4-%d-a.db", *index)), store.Identity{Network: n.ChainID, Role: "wallet", Node: "a", Schema: 4})
	if err != nil {
		return err
	}
	defer db0.Close()
	db1, err := store.Open(filepath.Join(*dir, fmt.Sprintf("wallet-v4-%d-b.db", *index)), store.Identity{Network: n.ChainID, Role: "wallet", Node: "b", Schema: 4})
	if err != nil {
		return err
	}
	defer db1.Close()
	w0, err := wallet.New(db0, n.Genesis.Network, desc0.Owner, n.Organizations)
	if err != nil {
		return err
	}
	w1, err := wallet.New(db1, n.Genesis.Network, desc1.Owner, n.Organizations)
	if err != nil {
		return err
	}
	httpClient := transport.NewHTTPClient(10 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	defer blockfollow.Start(ctx, cancel, db0, transport.NewCommitteeClient(n.CommitteeURLs[0]), trust, w0.ApplyBlock)()
	defer blockfollow.Start(ctx, cancel, db1, transport.NewCommitteeClient(n.CommitteeURLs[0]), trust, w1.ApplyBlock)()
	var sent time.Time
	send := func(org int, req protocol.DirectRequest) (protocol.OutputCertificate, error) {
		raw, err := req.MarshalBinary()
		if err != nil {
			return protocol.OutputCertificate{}, err
		}
		r, err := http.NewRequestWithContext(ctx, "POST", lab.Gateways[org]+"/v3/transactions", bytes.NewReader(raw))
		if err != nil {
			return protocol.OutputCertificate{}, err
		}
		r.Header.Set("Content-Type", transport.MediaType)
		sent = time.Now()
		resp, err := httpClient.Do(r)
		if err != nil {
			return protocol.OutputCertificate{}, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, protocol.MaxCertificateBytes))
		if err != nil {
			return protocol.OutputCertificate{}, err
		}
		if resp.StatusCode != 200 {
			return protocol.OutputCertificate{}, fmt.Errorf("gateway %d: %s", resp.StatusCode, b)
		}
		return protocol.DecodeOutputCertificate(b)
	}
	parent, err := build(n.Organizations[0], protocol.Input{Kind: protocol.FinalInput, Output: origin.ID, Evidence: origin.Fact}, origin.Output, desc1, 1)
	if err != nil {
		return err
	}
	req := protocol.DirectRequest{Tx: parent}
	if err = w0.SaveDirectRequest(req); err != nil {
		return err
	}
	sent = time.Now()
	var pc protocol.OutputCertificate
	if *withhold {
		var clients [4]gateway.MemberClient
		for i, url := range n.Members[n.Organizations[0].Org] {
			clients[i] = transport.NewMemberClient(url)
		}
		collector, err := gateway.New(n.Organizations[0], clients, db0)
		if err != nil {
			return err
		}
		pc, err = collector.CollectDirect(ctx, req)
		if err != nil {
			return err
		}
	} else {
		pc, err = send(0, req)
		if err != nil {
			return err
		}
	}
	if err = w1.ReceiveDirect(parent.Body.Outputs[0], pc, 0); err != nil {
		return err
	}
	parentFast := time.Since(sent)
	child, err := build(n.Organizations[1], protocol.Input{Kind: protocol.CertificateInput, Output: pc.Summary.OutputID(0), Evidence: protocol.Hash(pc.QC.Fact)}, parent.Body.Outputs[0], desc0, 2)
	if err != nil {
		return err
	}
	childReq := protocol.DirectRequest{Tx: child, InputCertificates: []protocol.InputCertificate{{Certificate: pc, Index: 0}}}
	if err = w1.SaveDirectRequest(childReq); err != nil {
		return err
	}
	sent = time.Now()
	cc, err := send(1, childReq)
	if err != nil {
		return err
	}
	if err = w0.ReceiveDirect(child.Body.Outputs[0], cc, 0); err != nil {
		return err
	}
	childFast := time.Since(sent)
	public := transport.NewCommitteeClient(n.CommitteeURLs[0])
	var finalElapsed time.Duration
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := w0.DirectFinal(cc.Summary.OutputID(0), 0)
		if err != nil {
			return err
		}
		if ok {
			finalElapsed = time.Since(sent)
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	report := map[string]any{"timing_origin": "wallet_http_submit_v4", "parent_fast_ms": float64(parentFast) / float64(time.Millisecond), "child_fast_ms": float64(childFast) / float64(time.Millisecond), "child_block_observed_ms": float64(finalElapsed) / float64(time.Millisecond), "parent_output": protocol.Hash(pc.Summary.OutputID(0)).String(), "child_output": protocol.Hash(cc.Summary.OutputID(0)).String(), "parent_withheld": *withhold}
	fmt.Println("Child finalized; waiting for compensation only when the parent is deliberately withheld.")
	if *withhold {
		url := n.CommitteeURLs[0] + "/v3/obligations/" + protocol.Hash(pc.Summary.OutputID(0)).String()
		for {
			resp, err := httpClient.Get(url)
			if err == nil {
				var ob rules.DirectObligation
				err = json.NewDecoder(resp.Body).Decode(&ob)
				resp.Body.Close()
				if err == nil && ob.Status == rules.DirectRepaired {
					report["compensation_observed_ms"] = float64(time.Since(sent)) / float64(time.Millisecond)
					break
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
		raw, err := (protocol.DirectPayment{Tx: parent, Certificate: pc}).MarshalBinary()
		if err != nil {
			return err
		}
		if err = public.Submit(ctx, raw); err != nil {
			return err
		}
		for {
			ok, err := w1.DirectFinal(pc.Summary.OutputID(0), 1)
			if err != nil {
				return err
			}
			if ok {
				report["late_parent_instance"] = 1
				break
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}
	path := filepath.Join(*dir, "reports", fmt.Sprintf("direct-%d.json", *index))
	if err = cfg.Write(path, report); err != nil {
		return err
	}
	pretty, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(pretty))
	return nil
}
