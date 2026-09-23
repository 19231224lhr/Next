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
	"os"
	"path/filepath"
	"sync"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/blockfollow"
	"utxo/internal/member"
	"utxo/internal/requesttrace"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type chainHop struct {
	Hop, Sender, Receiver                                                       int
	Tx                                                                          protocol.TxID
	Input, Output                                                               protocol.OutputID
	Fact                                                                        protocol.SpendFactID
	OutputData                                                                  protocol.Output
	CertificateInput                                                            bool
	RequestBytes, CertificateBytes, Attempts                                    int
	ProgressErrors                                                              int
	BuildStartUnixNS, BuildDoneUnixNS, SentUnixNS, ReadyUnixNS                  int64
	FinalUnixNS, MemberClosedUnixNS                                             int64
	BuildMS, FastMS, SentOffsetMS, ReadyOffsetMS, FinalOffsetMS, MemberOffsetMS float64
	Foreground                                                                  []requesttrace.Event `json:",omitempty"`
}

type chainReport struct {
	Mode                                      string
	Requested                                 int
	WalletNoSync                              bool
	StartedUnixNS                             int64
	FastChainMS, PublicChainMS, ClosedChainMS float64
	Hops                                      []*chainHop
	Error                                     string `json:",omitempty"`
}

// chainInput reads the coin actually received by this wallet. Finality takes
// precedence over a retained certificate; no ancestor bundle is manufactured.
func chainInput(id protocol.OutputID, coin wallet.DirectCoin) (protocol.Input, []protocol.InputCertificate, error) {
	in := protocol.Input{Output: id}
	if coin.Instance != 0 {
		return in, nil, protocol.ErrRule
	}
	if coin.Final != (protocol.Hash{}) {
		in.Kind, in.Evidence = protocol.FinalInput, coin.Final
		return in, nil, nil
	}
	if coin.Certificate == nil {
		return in, nil, state.ErrNotFound
	}
	in.Kind, in.Evidence = protocol.CertificateInput, protocol.Hash(coin.Certificate.QC.Fact)
	return in, []protocol.InputCertificate{{Certificate: *coin.Certificate, Index: coin.Index}}, nil
}

// Setup runs before any node starts. Existing policy semantics authorize each
// wallet separately; adding recipients must not bypass fee-subject checks.
func prepareChain(dir string, lab cfg.Lab, n cfg.Network) error {
	path := filepath.Join(dir, "keys", "chain-c.key")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return fmt.Errorf("chain fixture already exists: %s", path)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err = os.WriteFile(path, key, 0600); err != nil {
		return err
	}
	b, err := cfg.PrivateKey(lab.Owners[1])
	if err != nil {
		return err
	}
	org := n.Organizations[0]
	for _, k := range []ed25519.PrivateKey{b, key} {
		var owner protocol.PublicKey
		copy(owner[:], k[32:])
		account := protocol.Digest("POLICY", org.Org[:], owner[:])
		n.Genesis.Grants = append(n.Genesis.Grants, state.Grant{ID: protocol.Digest("CHAIN_POLICY", account[:]), Organization: org.Hash(), Key: protocol.ResourceKey{Kind: protocol.ResourcePolicy, Account: account, Version: 1}, Amount: 1_000_000_000_000, Subject: owner})
	}
	return cfg.Write(lab.Network, n)
}

func chainDirect(args []string) error {
	f := flag.NewFlagSet("chain-v4", flag.ContinueOnError)
	dir := f.String("dir", "", "fresh laboratory directory")
	length := f.Int("length", 10, "number of dependent payments, 1..100")
	waitFinal := f.Bool("wait-final", false, "wait for verified wallet finality before the next payment")
	trace := f.Bool("trace", false, "foreground diagnostic trace")
	prepare := f.Bool("prepare", false, "authorize three wallets before starting the laboratory")
	auditOnly := f.Bool("audit", false, "audit the completed chain against stopped committee stores")
	e5 := f.Bool("e5-owner-fuel", false, "E5: each wallet pays with its own final FUEL")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *dir == "" || *length < 1 || *length > 100 {
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
	if *prepare {
		if err := prepareChain(*dir, lab, n); err != nil {
			return err
		}
		if *e5 {
			return prepareE5Chain(*dir, lab, *length)
		}
		return nil
	}
	if *auditOnly {
		return auditChain(*dir, lab)
	}
	return runChain(*dir, lab, n, *length, *waitFinal, *trace, *e5)
}

func runChain(dir string, lab cfg.Lab, n cfg.Network, length int, waitFinal, trace bool, ownerFuel ...bool) (err error) {
	e5 := len(ownerFuel) > 0 && ownerFuel[0]
	var e5Samples []faultSample
	if e5 {
		defer func() {
			f, e := os.Create(filepath.Join(dir, "reports", "fault-v4.json"))
			if e != nil {
				if err == nil {
					err = e
				}
				return
			}
			defer f.Close()
			if e = json.NewEncoder(f).Encode(faultReport{Samples: e5Samples}); err == nil {
				err = e
			}
		}()
	}
	p, err := n.Direct.Policy(n.Schedule, n.Organizations)
	if err != nil {
		return err
	}
	trust, err := n.Trust()
	if err != nil {
		return err
	}
	org := n.Organizations[0]
	var keys [3]ed25519.PrivateKey
	var descriptors [3]protocol.ReceiveDescriptor
	var wallets [3]*wallet.Wallet
	var dbs [3]*store.Group
	for i, path := range []string{lab.Owners[0], lab.Owners[1], filepath.Join(dir, "keys", "chain-c.key")} {
		keys[i], err = cfg.PrivateKey(path)
		if err != nil {
			return err
		}
		descriptors[i] = protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: org.Org}, keys[i])
		db, e := store.OpenNoSync(filepath.Join(dir, fmt.Sprintf("chain-wallet-%d.db", i)), store.Identity{Network: n.ChainID, Role: "chain-wallet", Node: fmt.Sprint(i), Schema: 4})
		if e != nil {
			return e
		}
		g, e := store.NewGroup(db, 256, 64)
		if e != nil {
			db.Close()
			return e
		}
		dbs[i] = g
		defer g.Close()
		wallets[i], err = wallet.New(g, n.Genesis.Network, descriptors[i].Owner, n.Organizations)
		if err != nil {
			return err
		}
	}
	var origin *state.OriginOutput
	for i := range n.Genesis.Outputs {
		if n.Genesis.Outputs[i].Output.Asset == protocol.AssetCAL && n.Genesis.Outputs[i].Output.Recipient.Owner == descriptors[0].Owner {
			origin = &n.Genesis.Outputs[i]
			if !e5 {
				break
			} // E5's last CAL is separate from its first 100 warmup inputs.
		}
	}
	if origin == nil {
		return state.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := transport.NewHTTPClient(10 * time.Second)
	if e5 {
		client = transport.NewHTTPClient(2 * time.Second)
	}
	for i := range wallets {
		defer blockfollow.Start(ctx, cancel, dbs[i], transport.NewCommitteeClient(n.CommitteeURLs[0]), trust, wallets[i].PrepareBlock)()
	}
	batchCtx, stopBatches := context.WithCancel(ctx)
	var batches [4]*progressBatcher
	for i, url := range n.Members[org.Org] {
		batches[i] = newProgressBatcher(batchCtx, length, func(ctx context.Context, facts []protocol.SpendFactID) ([]member.DirectStatus, error) {
			return loadProgressBatch(ctx, client, url, facts)
		})
	}
	defer func() {
		stopBatches()
		for _, b := range batches {
			<-b.done
		}
	}()
	var observers sync.WaitGroup
	observerErrors := make(chan error, length)
	report := chainReport{Mode: "fast", Requested: length, WalletNoSync: true}
	if waitFinal {
		report.Mode = "wait_final"
	}
	var start time.Time
	defer func() {
		cancel()
		observers.Wait()
		if err == nil {
			select {
			case err = <-observerErrors:
			default:
			}
		}
		if len(report.Hops) > 0 {
			last := report.Hops[len(report.Hops)-1]
			report.FastChainMS, report.PublicChainMS = last.ReadyOffsetMS, last.FinalOffsetMS
			for _, h := range report.Hops {
				report.ClosedChainMS = max(report.ClosedChainMS, h.MemberOffsetMS)
			}
		}
		if err != nil {
			report.Error = err.Error()
		}
		if e := cfg.Write(filepath.Join(dir, "reports", "chain-v4.json"), report); err == nil {
			err = e
		}
		fmt.Printf("chain mode=%s hops=%d/%d fast=%.3fms public=%.3fms closed=%.3fms error=%q\n", report.Mode, len(report.Hops), length, report.FastChainMS, report.PublicChainMS, report.ClosedChainMS, report.Error)
	}()
	id := origin.ID
	var fuels [3][]state.OriginOutput
	var fuelIndex [3]int
	if e5 {
		fuelIndex[0] = 100
	} // The independent warmup pays with the first 100 user FUEL inputs.
	if e5 {
		for _, o := range n.Genesis.Outputs {
			if o.Output.Asset == protocol.AssetFUEL {
				for j, d := range descriptors {
					if o.Output.Recipient.Owner == d.Owner {
						fuels[j] = append(fuels[j], o)
					}
				}
			}
		}
	}
	for i := 0; i < length; i++ {
		h := &chainHop{Hop: i + 1, Sender: i % 3, Receiver: (i + 1) % 3, Input: id, BuildStartUnixNS: time.Now().UnixNano()}
		buildStart := time.Now()
		coin := wallet.DirectCoin{Output: origin.Output, Final: origin.Fact}
		if i > 0 {
			err = dbs[h.Sender].View(func(v state.ReadView) error {
				var found bool
				var e error
				coin, found, e = state.Load[wallet.DirectCoin](v, wallet.DirectCoinKey(id, 0))
				if e != nil {
					return e
				}
				if !found {
					return state.ErrNotFound
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		in, proofs, e := chainInput(id, coin)
		if e != nil {
			return e
		}
		h.CertificateInput = in.Kind == protocol.CertificateInput
		if waitFinal && h.CertificateInput {
			return fmt.Errorf("wait-final selected an unconfirmed input")
		}
		body := protocol.TxBody{Wire: 4, Version: 4, Network: n.Genesis.Network, Kind: protocol.FastTransfer, Subject: coin.Output.Recipient.Owner, Certifier: org.Org, Config: org.Hash(), Epoch: org.Epoch, Rules: p.Rules(), Inputs: []protocol.Input{in}, Outputs: []protocol.Output{{Asset: protocol.AssetCAL, Amount: coin.Output.Amount, Recipient: descriptors[h.Receiver]}}, Fee: protocol.FeeTerms{Source: protocol.OrgReserve, Account: org.Org, Version: 1, Maximum: 1000}, Work: protocol.WorkLimit{Execution: 100000, Bytes: 10000000, Depth: 1, Ancestors: 1}}
		for _, g := range n.Genesis.Grants {
			if g.Organization != org.Hash() || (g.Key.Kind == protocol.ResourcePolicy && g.Subject != body.Subject) {
				continue
			}
			body.Admission = append(body.Admission, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
			if g.Key.Kind == protocol.ResourcePolicy {
				body.Fee.Policy = g.Key.Account
			}
		}
		nonce := protocol.Digest("CHAIN_V4", id[:])
		copy(body.Nonce[:], nonce[:])
		body.Intent = body.IntentID()
		var tx protocol.FastTx
		var request protocol.DirectRequest
		if e5 {
			j := h.Sender
			if fuelIndex[j] >= len(fuels[j]) {
				return fmt.Errorf("E5 wallet FUEL fixture exhausted")
			}
			request, e = budgetRequest(n, p, org, keys[j], in, coin.Output, descriptors[h.Receiver], proofs, fuels[j][fuelIndex[j]])
			if e != nil {
				return e
			}
			fuelIndex[j]++
			tx = request.Tx
			body = tx.Body
		} else {
			tx, e = protocol.NewFastTx(body, []protocol.InputClaim{{Output: coin.Output}}, p.Key)
			if e != nil {
				return e
			}
			tx.Auth = []protocol.OwnerAuth{protocol.SignOwner(tx.ID(), keys[h.Sender])}
			request = protocol.DirectRequest{Tx: tx, InputCertificates: proofs}
		}
		if e = wallets[h.Sender].SaveDirectRequest(request); e != nil {
			return e
		}
		raw, e := request.MarshalBinary()
		if e != nil {
			return e
		}
		h.RequestBytes = len(raw)
		h.Tx = tx.ID()
		h.OutputData = body.Outputs[0]
		h.BuildDoneUnixNS = time.Now().UnixNano()
		h.BuildMS = float64(time.Since(buildStart)) / 1e6
		sent := time.Now()
		if start.IsZero() {
			start = sent
			report.StartedUnixNS = sent.UnixNano()
		}
		h.SentUnixNS = sent.UnixNano()
		h.SentOffsetMS = float64(sent.Sub(start)) / 1e6
		report.Hops = append(report.Hops, h)
		var b []byte
		var header http.Header
		var attempts int
		if e5 {
			call, stop := context.WithTimeout(ctx, 30*time.Second)
			var c *protocol.OutputCertificate
			c, attempts, e = faultSend(call, client, lab.Gateways[0]+"/v3/transactions", raw, true)
			stop()
			if e == nil {
				b, e = c.MarshalBinary()
			}
		} else {
			b, header, attempts, e = submitChain(ctx, client, lab.Gateways[0]+"/v3/transactions", raw, trace)
		}
		h.Attempts = attempts
		if e != nil {
			return fmt.Errorf("hop %d: %w", i+1, e)
		}
		if trace {
			tc := requesttrace.Start(ctx, "wallet")
			requesttrace.Import(tc, header.Get(requesttrace.HeaderName), "")
			h.Foreground = requesttrace.Events(tc)
		}
		cert, e := protocol.DecodeOutputCertificate(b)
		if e != nil {
			return e
		}
		if e = wallets[h.Receiver].ReceiveDirect(h.OutputData, cert, 0); e != nil {
			return e
		}
		ready := time.Now()
		h.ReadyUnixNS = ready.UnixNano()
		h.FastMS = float64(ready.Sub(sent)) / 1e6
		h.ReadyOffsetMS = float64(ready.Sub(start)) / 1e6
		h.Fact = cert.QC.Fact
		if e5 {
			e5Samples = append(e5Samples, faultSample{Index: i, Request: request, Certificate: &cert, SentNS: h.SentUnixNS, ReadyNS: h.ReadyUnixNS})
		}
		h.Output = cert.Summary.OutputID(0)
		h.CertificateBytes = len(b)
		id = h.Output
		finalReady := make(chan struct{})
		observers.Add(1)
		go func(h *chainHop) {
			defer observers.Done()
			if e := observeChainHop(ctx, batches, wallets[h.Receiver], h, start, finalReady); e != nil {
				observerErrors <- e
				cancel()
			}
		}(h)
		if waitFinal {
			select {
			case <-finalReady:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	observers.Wait()
	select {
	case err = <-observerErrors:
		return err
	default:
		return nil
	}
}

// Wallet and members follow independently. A final coin can reach the wallet
// first; retry temporary quorum unavailability using identical signed bytes.
// The caller starts its timer before this function, including all retry waits.
func submitChain(ctx context.Context, client *http.Client, url string, raw []byte, trace bool) ([]byte, http.Header, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(raw))
		if err != nil {
			return nil, nil, attempt, err
		}
		req.Header.Set("Content-Type", transport.MediaType)
		if trace {
			req.Header.Set(requesttrace.HeaderName, "1")
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, attempt, err
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, protocol.MaxCertificateBytes))
		resp.Body.Close()
		if err != nil {
			return nil, nil, attempt, err
		}
		if resp.StatusCode == http.StatusOK {
			return b, resp.Header, attempt, nil
		}
		if resp.StatusCode != http.StatusServiceUnavailable || !bytes.Equal(bytes.TrimSpace(b), []byte("QUORUM_UNAVAILABLE")) {
			return nil, nil, attempt, fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, b)
		}
		select {
		case <-ctx.Done():
			return nil, nil, attempt, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func observeChainHop(ctx context.Context, batches [4]*progressBatcher, w *wallet.Wallet, h *chainHop, start time.Time, finalReady chan<- struct{}, instance ...uint8) error {
	var outputInstance uint8
	if len(instance) > 0 {
		outputInstance = instance[0]
	}
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		ok, err := w.DirectFinal(h.Output, outputInstance)
		if err != nil {
			return err
		}
		if ok {
			now := time.Now()
			h.FinalUnixNS = now.UnixNano()
			h.FinalOffsetMS = float64(now.Sub(start)) / 1e6
			close(finalReady)
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	var completed [4]bool
	for {
		all := true
		for i, b := range batches {
			if completed[i] {
				continue
			}
			status, err := b.Check(ctx, h.Fact)
			if err != nil {
				h.ProgressErrors++
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
			completed[i] = err == nil && status.Observed && (!status.Signed || status.Closed)
			all = all && completed[i]
		}
		if all {
			now := time.Now()
			h.MemberClosedUnixNS = now.UnixNano()
			h.MemberOffsetMS = float64(now.Sub(start)) / 1e6
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func auditChain(dir string, lab cfg.Lab) error {
	var report chainReport
	var obligations []rules.DirectObligation
	if err := cfg.Read(filepath.Join(dir, "reports", "chain-v4.json"), &report); err != nil {
		return err
	}
	if report.Error != "" || len(report.Hops) != report.Requested {
		return fmt.Errorf("incomplete chain")
	}
	for _, node := range lab.Nodes {
		if node.Binary != "committee" {
			continue
		}
		err := store.Inspect(filepath.Join(dir, node.Name, "committee.db"), func(v state.ReadView) error {
			for i, h := range report.Hops {
				if h.ReadyUnixNS == 0 || h.FinalUnixNS == 0 || h.MemberClosedUnixNS == 0 {
					return fmt.Errorf("hop %d incomplete observations", i+1)
				}
				if i > 0 && (h.Input != report.Hops[i-1].Output || h.BuildStartUnixNS < report.Hops[i-1].ReadyUnixNS) {
					return fmt.Errorf("hop %d did not spend actual previous receipt", i+1)
				}
				spent, found, e := state.Load[state.Spend](v, rules.DirectSpendKey(h.Input, 0))
				if e != nil {
					return e
				}
				if !found || spent.Consumed != h.Fact {
					return fmt.Errorf("hop %d input consumption mismatch", i+1)
				}
				out, found, e := state.Load[state.Creation](v, rules.DirectCreationKey(h.Output, 0))
				if e != nil {
					return e
				}
				if !found || !out.Final || out.Output != h.OutputData {
					return fmt.Errorf("hop %d output mismatch", i+1)
				}
				ob, found, e := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(h.Output))
				if e != nil {
					return e
				}
				if found && ob.Status != rules.DirectFulfilled {
					return fmt.Errorf("hop %d unresolved or repaired obligation", i+1)
				}
				if found && node.Name == "committee0" {
					obligations = append(obligations, ob)
				}
				if i == len(report.Hops)-1 {
					last, _, e := state.Load[state.Spend](v, rules.DirectSpendKey(h.Output, 0))
					if e != nil {
						return e
					}
					if last.Consumed != (protocol.SpendFactID{}) {
						return fmt.Errorf("last coin already spent")
					}
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("%s: %w", node.Name, err)
		}
	}
	result := map[string]any{"hops": len(report.Hops), "verified": true, "obligations": obligations, "checks": "exact chain links, receive-before-build, every input consumed once, final owner/output, no open or repaired obligations"}
	b, _ := json.Marshal(result)
	fmt.Println(string(b))
	return cfg.Write(filepath.Join(dir, "reports", "chain-audit.json"), result)
}
