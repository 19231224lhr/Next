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
	"sort"
	"sync"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/finality"
	"utxo/internal/rules"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

// This is a bounded closed-loop experiment, not a maximum-throughput claim.
// Each worker has one outstanding payment and observes its own receipts.
func benchDirect(args []string) error {
	flags := flag.NewFlagSet("bench-v3", flag.ContinueOnError)
	dir := flags.String("dir", "", "v3 laboratory")
	start := flags.Int("start", 100, "unused genesis input index")
	count := flags.Int("count", 128, "transactions")
	concurrency := flags.Int("concurrency", 16, "outstanding payments")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *start < 0 || *count < 1 || *count > 10000 || *concurrency < 1 || *concurrency > 256 {
		return protocol.ErrRule
	}
	var lab cfg.Lab
	if err := cfg.Read(filepath.Join(*dir, "lab.json"), &lab); err != nil {
		return err
	}
	var n cfg.Network
	if err := cfg.Read(lab.Network, &n); err != nil {
		return err
	}
	if n.Direct == nil {
		return protocol.ErrRule
	}
	policy, err := n.Direct.Policy(n.Schedule, n.Organizations)
	if err != nil {
		return err
	}
	trust, err := n.Trust()
	if err != nil {
		return err
	}
	owner, err := cfg.PrivateKey(lab.Owners[0])
	if err != nil {
		return err
	}
	recipient, err := cfg.PrivateKey(lab.Owners[1])
	if err != nil {
		return err
	}
	org := n.Organizations[0]
	target := protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: n.Organizations[1].Org}, recipient)
	var publicOwner protocol.PublicKey
	copy(publicOwner[:], owner[32:])
	var inputs []int
	for i, o := range n.Genesis.Outputs {
		if o.Output.Recipient.Owner == publicOwner {
			inputs = append(inputs, i)
		}
	}
	if *start+*count > len(inputs) {
		return protocol.ErrRule
	}
	db, err := store.Open(filepath.Join(*dir, fmt.Sprintf("bench-v3-%d.db", *start)), store.Identity{Network: n.ChainID, Role: "bench", Node: fmt.Sprint(*start), Schema: 3})
	if err != nil {
		return err
	}
	group, err := store.NewGroup(db, 256, 64)
	if err != nil {
		db.Close()
		return err
	}
	defer group.Close()
	sender, err := wallet.New(group, n.Genesis.Network, publicOwner, n.Organizations)
	if err != nil {
		return err
	}
	receiver, err := wallet.New(group, n.Genesis.Network, target.Owner, n.Organizations)
	if err != nil {
		return err
	}
	requests := make([]protocol.DirectRequest, *count)
	encoded := make([][]byte, *count)
	for i := range requests {
		origin := n.Genesis.Outputs[inputs[*start+i]]
		body := protocol.TxBody{Wire: 3, Version: 3, Network: n.Genesis.Network, Kind: protocol.FastTransfer, Subject: publicOwner, Certifier: org.Org, Config: org.Hash(), Epoch: org.Epoch, Rules: policy.Rules(), Inputs: []protocol.Input{{Kind: protocol.FinalInput, Output: origin.ID, Evidence: origin.Fact}}, Outputs: []protocol.Output{{Asset: protocol.AssetCAL, Amount: origin.Output.Amount, Recipient: target}}, Fee: protocol.FeeTerms{Source: protocol.OrgReserve, Account: org.Org, Version: 1, Maximum: 1000}, Work: protocol.WorkLimit{Execution: 100000, Bytes: 10000000, Depth: 1, Ancestors: 1}}
		for _, g := range n.Genesis.Grants {
			if g.Organization == org.Hash() {
				body.Admission = append(body.Admission, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
				if g.Key.Kind == protocol.ResourcePolicy {
					body.Fee.Policy = g.Key.Account
				}
			}
		}
		nonce := protocol.Digest("BENCH_V3", origin.ID[:])
		copy(body.Nonce[:], nonce[:])
		body.Intent = body.IntentID()
		tx, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: origin.Output}}, policy.Key)
		if err != nil {
			return err
		}
		tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), owner)}
		requests[i] = protocol.DirectRequest{Tx: tx}
		if err = sender.SaveDirectRequest(requests[i]); err != nil {
			return err
		}
		encoded[i], err = requests[i].MarshalBinary()
		if err != nil {
			return err
		}
	}
	type sample struct {
		Index                                     int
		FastMS, ProofObservedMS, CreditObservedMS float64
		Fact                                      string
		Error                                     string `json:",omitempty"`
	}
	samples := make([]sample, *count)
	httpClient := transport.NewHTTPClient(10 * time.Second)
	public := transport.NewCommitteeClient(n.CommitteeURLs[0])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	jobs := make(chan int, *count)
	for i := 0; i < *count; i++ {
		jobs <- i
	}
	close(jobs)
	var wg sync.WaitGroup
	began := time.Now()
	for worker := 0; worker < *concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				samples[i].Index = *start + i
				run := func() error {
					req, err := http.NewRequestWithContext(ctx, "POST", lab.Gateways[0]+"/v3/transactions", bytes.NewReader(encoded[i]))
					if err != nil {
						return err
					}
					req.Header.Set("Content-Type", transport.MediaType)
					sent := time.Now()
					resp, err := httpClient.Do(req)
					if err != nil {
						return err
					}
					raw, err := io.ReadAll(io.LimitReader(resp.Body, protocol.MaxCertificateBytes))
					resp.Body.Close()
					if err != nil {
						return err
					}
					if resp.StatusCode != 200 {
						return fmt.Errorf("gateway %d: %s", resp.StatusCode, raw)
					}
					cert, err := protocol.DecodeOutputCertificate(raw)
					if err != nil {
						return err
					}
					if err = receiver.ReceiveDirect(requests[i].Tx.Body.Outputs[0], cert, 0); err != nil {
						return err
					}
					samples[i].FastMS = float64(time.Since(sent)) / float64(time.Millisecond)
					samples[i].Fact = protocol.Hash(cert.QC.Fact).String()
					ticker := time.NewTicker(25 * time.Millisecond)
					defer ticker.Stop()
					created := false
					for {
						if !created {
							proof, err := public.Receipt(ctx, protocol.FactOutputCreated, protocol.Hash(cert.Summary.OutputID(0)))
							if err == nil {
								if err = receiver.FinalizeDirect(trust, proof); err != nil {
									return err
								}
								created = true
								samples[i].ProofObservedMS = float64(time.Since(sent)) / float64(time.Millisecond)
							}
						}
						proofs, err := public.DirectReceipts(ctx, cert.QC.Fact)
						complete := false
						if err == nil {
							var facts []protocol.FinalFact
							for _, proof := range proofs {
								verified, err := finality.Verify(trust, proof)
								if err != nil {
									return err
								}
								facts = append(facts, verified.Fact())
							}
							complete, err = rules.CheckDirectCredits(cert.Summary, facts)
							if err != nil {
								return err
							}
						}
						if created && complete {
							samples[i].CreditObservedMS = float64(time.Since(sent)) / float64(time.Millisecond)
							return nil
						}
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-ticker.C:
						}
					}
				}
				if err := run(); err != nil {
					samples[i].Error = err.Error()
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(began)
	fast := []float64{}
	proof := []float64{}
	credit := []float64{}
	failed := 0
	for _, s := range samples {
		if s.Error != "" {
			failed++
			continue
		}
		fast = append(fast, s.FastMS)
		proof = append(proof, s.ProofObservedMS)
		credit = append(credit, s.CreditObservedMS)
	}
	quantile := func(xs []float64, q float64) float64 {
		if len(xs) == 0 {
			return 0
		}
		sort.Float64s(xs)
		return xs[int(float64(len(xs)-1)*q)]
	}
	summary := map[string]any{"timing_origin": "wallet_http_submit_v3", "workload": "closed_loop_final_utxo_cross_org", "count": *count, "concurrency": *concurrency, "failed": failed, "elapsed_s": elapsed.Seconds(), "completed_per_second": float64(*count-failed) / elapsed.Seconds(), "fast_p50_ms": quantile(fast, .5), "fast_p95_ms": quantile(fast, .95), "proof_observed_p50_ms": quantile(proof, .5), "credit_observed_p50_ms": quantile(credit, .5), "note": "Proof/credit figures include polling and client verification; member application drain is checked separately by stopped audit."}
	report := struct {
		Summary map[string]any
		Samples []sample
	}{summary, samples}
	path := filepath.Join(*dir, "reports", fmt.Sprintf("bench-v3-%d.json", *start))
	if err = cfg.Write(path, report); err != nil {
		return err
	}
	pretty, _ := json.MarshalIndent(summary, "", "  ")
	fmt.Println(string(pretty))
	if failed != 0 {
		return fmt.Errorf("%d payments failed; see %s", failed, path)
	}
	return nil
}
