package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/finality"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type measurement struct {
	Lane, Sequence             int
	Spend                      string
	Started                    time.Time
	Fast, Ready, Proof, Closed time.Duration
	ProofQueue, CreditQueue    time.Duration
	Error                      string
}
type job struct {
	sample measurement
	cert   protocol.TXCer
}
type runner struct {
	lab     cfg.Lab
	network cfg.Network
	trust   finality.Trust
	keys    [2]ed25519.PrivateKey
	wallets [2]*wallet.Wallet
	http    *http.Client
	public  *transport.CommitteeClient
	ctx     context.Context
}

func (r *runner) request(lane, sequence int, input protocol.Input, parent *protocol.TXCer) (job, error) {
	sender := sequence % 2
	receiver := 1 - sender
	org := r.network.Organizations[sender]
	descriptor := protocol.NewDescriptor(r.network.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: r.network.Organizations[receiver].Org}, r.keys[receiver])
	var subject protocol.PublicKey
	copy(subject[:], r.keys[sender][32:])
	body := protocol.TxBody{Wire: 2, Version: 2, Network: r.network.Genesis.Network, Kind: protocol.FastTransfer, Subject: subject, Certifier: org.Org, Epoch: org.Epoch, Config: org.Hash(), Rules: r.network.Schedule.IDs(), Inputs: []protocol.Input{input}, Outputs: []protocol.Output{{Asset: protocol.AssetCAL, Amount: 100, Recipient: descriptor}}, Fee: protocol.FeeTerms{Source: protocol.OrgReserve, Account: org.Org, Version: 1, Maximum: 100}, Work: protocol.WorkLimit{Execution: 10000, Bytes: 1 << 20, Depth: 64, Ancestors: 256}}
	if _, e := rand.Read(body.Nonce[:]); e != nil {
		return job{}, e
	}
	for _, g := range r.network.Genesis.Grants {
		if g.Organization == org.Hash() {
			body.Admission = append(body.Admission, protocol.AdmissionRef{Key: g.Key, Grant: g.ID})
			if g.Key.Kind == protocol.ResourcePolicy {
				body.Fee.Policy = g.Key.Account
			}
		}
	}
	body.Intent = body.IntentID()
	request := protocol.PaymentRequest{Tx: protocol.SignedTx{Body: body, Auth: []protocol.OwnerAuth{protocol.SignOwner(body.ID(), r.keys[sender])}}}
	if parent != nil {
		request.Parents = []protocol.TXCer{*parent}
	}
	result := job{sample: measurement{Lane: lane, Sequence: sequence}}
	if e := r.wallets[sender].SaveRequest(request); e != nil {
		return result, e
	}
	raw, e := request.MarshalBinary()
	if e != nil {
		return result, e
	}
	req, e := http.NewRequestWithContext(r.ctx, http.MethodPost, r.lab.Gateways[sender]+"/v1/transactions", bytes.NewReader(raw))
	if e != nil {
		return result, e
	}
	req.Header.Set("Content-Type", transport.MediaType)
	// Measure from wallet HTTP submission, after durable preparation and encoding.
	result.sample.Started = time.Now()
	response, e := r.http.Do(req)
	if e != nil {
		return result, e
	}
	raw, e = io.ReadAll(io.LimitReader(response.Body, protocol.MaxCertificateBytes+1))
	response.Body.Close()
	if e != nil {
		return result, e
	}
	if response.StatusCode != 200 {
		return result, fmt.Errorf("gateway HTTP %d: %s", response.StatusCode, raw)
	}
	c, e := protocol.DecodeCertificate(raw)
	if e != nil {
		return result, e
	}
	if e = c.Verify(org); e != nil {
		return result, e
	}
	result.sample.Fast = time.Since(result.sample.Started)
	result.cert = c
	result.sample.Spend = protocol.Hash(c.QC.Fact).String()
	if e = r.wallets[receiver].Receive(c, 0); e != nil {
		return result, e
	}
	result.sample.Ready = time.Since(result.sample.Started)
	return result, nil
}
func (r *runner) anchor(c protocol.TXCer, nextOrg int) (protocol.Input, error) {
	for r.ctx.Err() == nil {
		proof, e := r.public.Receipt(r.ctx, protocol.FactOutputCreated, protocol.Hash(c.Effects.Outputs[0]))
		if e == nil {
			verified, e := finality.Verify(r.trust, proof)
			if e != nil {
				return protocol.Input{}, e
			}
			output, e := protocol.DecodeSettledOutput(verified.Fact().Payload)
			if e != nil || output.ID != c.Effects.Outputs[0] {
				return protocol.Input{}, protocol.ErrAuth
			}
			for _, url := range r.network.Members[r.network.Organizations[nextOrg].Org] {
				client := transport.NewMemberClient(url)
				client.HTTP = r.http
				if e = client.ApplyProof(r.ctx, proof); e != nil {
					return protocol.Input{}, e
				}
			}
			return protocol.Input{Kind: protocol.FinalInput, Output: output.ID, Evidence: verified.Fact().ID()}, nil
		}
		select {
		case <-r.ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	return protocol.Input{}, r.ctx.Err()
}
func (r *runner) trackProof(j job) measurement {
	m := j.sample
	m.ProofQueue = time.Since(m.Started) - m.Ready
	c := j.cert
	// Verify FeeClosed before treating a payment as publicly complete.
	for r.ctx.Err() == nil {
		proof, e := r.public.Receipt(r.ctx, protocol.FactFeeClosed, protocol.Hash(c.Effects.Fee))
		if e == nil {
			verified, e := finality.Verify(r.trust, proof)
			if e != nil {
				m.Error = e.Error()
				return m
			}
			f := verified.Fact()
			if f.Kind != protocol.FactFeeClosed || f.Key != protocol.Hash(c.Effects.Fee) || f.Rules != c.Tx.Body.Rules {
				m.Error = "wrong fee proof"
				return m
			}
			m.Proof = time.Since(m.Started)
			break
		}
		select {
		case <-r.ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	if m.Proof == 0 {
		m.Error = "final proof timeout"
		return m
	}
	return m
}
func (r *runner) trackCredit(j job) measurement {
	m := j.sample
	m.CreditQueue = time.Since(m.Started) - m.Ready
	c := j.cert
	// These node observations measure application progress, not financial proof.
	for r.ctx.Err() == nil {
		complete := true
		for _, url := range r.network.Members[c.Tx.Body.Certifier] {
			req, e := http.NewRequestWithContext(r.ctx, http.MethodGet, url+"/v1/outcomes/"+m.Spend, nil)
			if e != nil {
				m.Error = e.Error()
				return m
			}
			response, e := r.http.Do(req)
			if e != nil {
				complete = false
				break
			}
			var out struct {
				Approved, PublicObserved, Custody                              bool
				FuelResidual, PolicyResidual, ExecutionResidual, BytesResidual string
			}
			err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&out)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 || !out.PublicObserved || !out.Custody || (out.Approved && (out.FuelResidual != "94" || out.PolicyResidual != "94" || out.ExecutionResidual != "0" || out.BytesResidual != "0")) {
				complete = false
				break
			}
		}
		if complete {
			m.Closed = time.Since(m.Started)
			return m
		}
		select {
		case <-r.ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	m.Error = "member credit drain timeout"
	return m
}
func quantiles(samples []measurement, pick func(measurement) time.Duration) map[string]float64 {
	var values []float64
	for _, m := range samples {
		if v := pick(m); v > 0 {
			values = append(values, float64(v)/float64(time.Millisecond))
		}
	}
	sort.Float64s(values)
	result := map[string]float64{}
	if len(values) == 0 {
		return result
	}
	for label, p := range map[string]float64{"p50_ms": 0.5, "p95_ms": 0.95, "p99_ms": 0.99} {
		i := int(float64(len(values)-1) * p)
		result[label] = values[i]
	}
	return result
}
func run() error {
	dir := flag.String("dir", "", "running lab")
	duration := flag.Duration("duration", 10*time.Second, "generation duration")
	drain := flag.Duration("drain", 90*time.Second, "completion drain limit")
	lanes := flag.Int("lanes", 8, "parallel closed-loop chains")
	count := flag.Int("transactions-per-lane", 0, "fixed transfers per chain; zero runs for duration")
	offset := flag.Int("input", 0, "first unused genesis input index")
	flag.Parse()
	if *lanes < 1 || *lanes > 256 || *duration <= 0 || *drain <= 0 || *count < 0 {
		return fmt.Errorf("invalid benchmark bounds")
	}
	var lab cfg.Lab
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
	httpTransport := http.DefaultTransport.(*http.Transport).Clone()
	httpTransport.MaxIdleConns = 512
	httpTransport.MaxIdleConnsPerHost = 128
	httpTransport.MaxConnsPerHost = 256
	client := &http.Client{Transport: httpTransport, Timeout: 10 * time.Second}
	defer httpTransport.CloseIdleConnections()
	r := runner{lab: lab, network: network, trust: trust, http: client, public: transport.NewCommitteeClient(network.CommitteeURLs[0])}
	r.public.HTTP = client
	for i, path := range lab.Owners {
		r.keys[i], e = cfg.PrivateKey(path)
		if e != nil {
			return e
		}
		db, e := store.Open(filepath.Join(*dir, fmt.Sprintf("wallet%d.db", i)), store.Identity{Network: network.Genesis.Network.String(), Role: "wallet", Node: fmt.Sprint(i), Schema: 2})
		if e != nil {
			return e
		}
		group, e := store.NewGroup(db, 512, 64)
		if e != nil {
			db.Close()
			return e
		}
		defer group.Close()
		stopRelay, err := network.StartWalletRelay(group)
		if err != nil {
			return err
		}
		defer stopRelay()
		var owner protocol.PublicKey
		copy(owner[:], r.keys[i][32:])
		r.wallets[i], e = wallet.New(group, network.Genesis.Network, owner, network.Organizations)
		if e != nil {
			return e
		}
	}
	var origins []state.OriginOutput
	for _, o := range network.Genesis.Outputs {
		if bytes.Equal(o.Output.Recipient.Owner[:], r.keys[0][32:]) {
			origins = append(origins, o)
		}
	}
	if *offset < 0 || *offset+*lanes > len(origins) {
		return fmt.Errorf("insufficient unused initial inputs")
	}
	for _, node := range lab.Nodes {
		response, e := client.Get(node.URL + "/healthz")
		if e != nil {
			return e
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			return fmt.Errorf("%s unhealthy", node.Name)
		}
	}
	started := time.Now()
	end := started.Add(*duration)
	ctx, cancel := context.WithDeadline(context.Background(), end.Add(*drain))
	defer cancel()
	r.ctx = ctx
	proofJobs := make(chan job, *lanes*16)
	creditJobs := make(chan job, *lanes*16)
	var trackWG, generateWG sync.WaitGroup
	var mu sync.Mutex
	var samples []measurement
	indices := make(map[string]int)
	appendSample := func(m measurement) {
		mu.Lock()
		defer mu.Unlock()
		if i, ok := indices[m.Spend]; ok && m.Spend != "" {
			saved := &samples[i]
			if m.Proof > 0 {
				saved.Proof = m.Proof
			}
			if m.Closed > 0 {
				saved.Closed = m.Closed
			}
			if m.ProofQueue > 0 {
				saved.ProofQueue = m.ProofQueue
			}
			if m.CreditQueue > 0 {
				saved.CreditQueue = m.CreditQueue
			}
			if m.Error != "" {
				if saved.Error != "" {
					saved.Error += "; "
				}
				saved.Error += m.Error
			}
		} else {
			if m.Spend != "" {
				indices[m.Spend] = len(samples)
			}
			samples = append(samples, m)
		}
	}
	for _, observer := range []struct {
		jobs  <-chan job
		track func(job) measurement
	}{
		{proofJobs, r.trackProof}, {creditJobs, r.trackCredit},
	} {
		for i := 0; i < *lanes*2; i++ {
			trackWG.Add(1)
			go func(jobs <-chan job, track func(job) measurement) {
				defer trackWG.Done()
				for j := range jobs {
					appendSample(track(j))
				}
			}(observer.jobs, observer.track)
		}
	}
	for lane := 0; lane < *lanes; lane++ {
		generateWG.Add(1)
		go func(lane int) {
			defer generateWG.Done()
			origin := origins[*offset+lane]
			input := protocol.Input{Kind: protocol.FinalInput, Output: origin.ID, Evidence: origin.Fact}
			var parent *protocol.TXCer
			for sequence := 0; ((*count == 0 && time.Now().Before(end)) || (*count > 0 && sequence < *count)) && ctx.Err() == nil; sequence++ {
				j, e := r.request(lane, sequence, input, parent)
				if e != nil {
					j.sample.Error = e.Error()
					appendSample(j.sample)
					return
				}
				for _, queue := range []chan job{proofJobs, creditJobs} {
					select {
					case queue <- j:
					case <-ctx.Done():
						j.sample.Error = "completion queue timed out"
						appendSample(j.sample)
						return
					}
				}
				copy := j.cert
				parent = &copy
				input = protocol.Input{Kind: protocol.CertificateInput, Output: copy.Effects.Outputs[0], Evidence: protocol.Hash(copy.QC.Fact)}
				if (sequence+1)%16 == 0 && ((*count == 0 && time.Now().Before(end)) || (*count > 0 && sequence+1 < *count)) {
					input, e = r.anchor(copy, (sequence+1)%2)
					if e != nil {
						appendSample(measurement{Lane: lane, Sequence: sequence, Started: time.Now(), Error: "anchor: " + e.Error()})
						return
					}
					parent = nil
				}
			}
		}(lane)
	}
	generateWG.Wait()
	generationFinished := time.Now()
	close(proofJobs)
	close(creditJobs)
	trackWG.Wait()
	finished := time.Now()
	sort.Slice(samples, func(i, j int) bool { return samples[i].Started.Before(samples[j].Started) })
	fast, proof, closed, failures := 0, 0, 0, 0
	fastWindow, proofWindow, closedWindow := 0, 0, 0
	for _, m := range samples {
		if m.Ready > 0 {
			fast++
			if m.Started.Add(m.Ready).Before(end) {
				fastWindow++
			}
		}
		if m.Proof > 0 {
			proof++
			if m.Started.Add(m.Proof).Before(end) {
				proofWindow++
			}
		}
		if m.Closed > 0 {
			closed++
			if m.Started.Add(m.Closed).Before(end) {
				closedWindow++
			}
		}
		if m.Error != "" {
			failures++
		}
	}
	prefix := filepath.Join(*dir, "reports", fmt.Sprintf("bench-%d", started.UnixNano()))
	file, e := os.OpenFile(prefix+".csv", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	csvWriter := csv.NewWriter(file)
	_ = csvWriter.Write([]string{"lane", "sequence", "spend", "start_unix_ns", "fast_us", "wallet_ready_us", "final_proof_us", "credit_closed_us", "proof_queue_us", "credit_queue_us", "error"})
	for _, m := range samples {
		_ = csvWriter.Write([]string{strconv.Itoa(m.Lane), strconv.Itoa(m.Sequence), m.Spend, strconv.FormatInt(m.Started.UnixNano(), 10), strconv.FormatInt(m.Fast.Microseconds(), 10), strconv.FormatInt(m.Ready.Microseconds(), 10), strconv.FormatInt(m.Proof.Microseconds(), 10), strconv.FormatInt(m.Closed.Microseconds(), 10), strconv.FormatInt(m.ProofQueue.Microseconds(), 10), strconv.FormatInt(m.CreditQueue.Microseconds(), 10), m.Error})
	}
	csvWriter.Flush()
	e = csvWriter.Error()
	closeErr := file.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	report := map[string]any{
		"transactions_per_lane": *count, "generation_elapsed_seconds": generationFinished.Sub(started).Seconds(),
		"proof_observer_queue":  quantiles(samples, func(m measurement) time.Duration { return m.ProofQueue }),
		"credit_observer_queue": quantiles(samples, func(m measurement) time.Duration { return m.CreditQueue }),
		"timing_origin":         "wallet_http_submit_v2",
		"timing_scope":          "Immediately before HTTP Client.Do through recipient certificate verification and synchronous wallet persistence; excludes sender preparation, signing, persistence and encoding. HTTP transport waiting and connection setup are included. Proof and credit observations use the same origin.",
		"network":               network.ChainID, "lanes": *lanes, "generation_seconds": duration.Seconds(), "total_seconds": finished.Sub(started).Seconds(), "finite_initial_fuel": "2000000000000",
		"ready": fast, "public_proven": proof, "credit_closed": closed, "failures": failures,
		"ready_tps_in_generation_window": float64(fastWindow) / duration.Seconds(), "public_proof_tps_in_generation_window": float64(proofWindow) / duration.Seconds(), "credit_closed_tps_in_generation_window": float64(closedWindow) / duration.Seconds(),
		"ready_latency": quantiles(samples, func(m measurement) time.Duration { return m.Ready }), "proof_observed_latency": quantiles(samples, func(m measurement) time.Duration { return m.Proof }), "credit_observed_latency": quantiles(samples, func(m measurement) time.Duration { return m.Closed }),
		"conditions": "14 independent processes on one host; synchronous bbolt; cross-organization 100-CAL chains; final-anchor import each 16 transfers; no warmup exclusion; independent bounded proof and credit observers; each includes its separately reported queue and polling delay; node observations are not financial proofs; FUEL cost 94 per transaction is retained.",
	}
	if e = cfg.Write(prefix+".json", report); e != nil {
		return e
	}
	raw, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(raw))
	fmt.Println("Raw samples:", prefix+".csv")
	if failures > 0 || closed != fast || proof != fast || (*count > 0 && fast != *count**lanes) {
		return fmt.Errorf("benchmark incomplete: ready=%d closed=%d failures=%d", fast, closed, failures)
	}
	return nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
