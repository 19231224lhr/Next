//go:build ignore

// Export public, successful independent payments from a stopped lab, then send
// the unchanged bytes to a fresh committee with the same genesis/configuration.
// HTTP 202 measures admission only; the caller must independently audit commits.
package main

import (
	"bytes"
	"crypto/rsa"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	cstate "github.com/cometbft/cometbft/state"
	"github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"utxo/crypto/chameleon"
	"utxo/internal/transport"
	"utxo/protocol"
)

type fixtureRow struct {
	Raw    []byte `json:"raw"` // JSON encodes bytes as base64; includes delivery wrapper.
	Fact   string `json:"fact"`
	Height int64  `json:"height"`
}

func main() {
	var err error
	switch {
	case len(os.Args) == 4 && os.Args[1] == "export":
		err = exportFixture(os.Args[2], os.Args[3])
	case len(os.Args) == 7 && os.Args[1] == "send":
		var count int
		var rate float64
		count, err = strconv.Atoi(os.Args[4])
		if err == nil {
			rate, err = strconv.ParseFloat(os.Args[5], 64)
		}
		if err == nil {
			err = sendFixture(os.Args[2], os.Args[3], count, rate, os.Args[6])
		}
	default:
		err = fmt.Errorf("usage: committee-load export <stopped-lab> <fixture.jsonl> | send <fixture> <URL> <count> <rate; 0=saturation> <out.json>")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func decodePayment(raw []byte) (protocol.DirectSubmission, error) {
	if wrapper, err := protocol.DecodeSubmission(raw); err == nil {
		raw = wrapper.Body
	}
	return protocol.DecodeDirectSubmission(raw)
}

func exportFixture(lab, output string) error {
	raw, err := os.ReadFile(filepath.Join(lab, "config/network.json"))
	if err != nil {
		return err
	}
	var network struct {
		ChainID string
		Direct  struct{ Modulus []byte }
		Genesis struct {
			Outputs []struct {
				ID     protocol.OutputID
				Fact   protocol.Hash
				Output protocol.Output
			}
		}
	}
	if err = json.Unmarshal(raw, &network); err != nil {
		return err
	}
	key, err := chameleon.NewPublic(&rsa.PublicKey{N: new(big.Int).SetBytes(network.Direct.Modulus), E: 65537})
	if err != nil {
		return err
	}
	if err = types.ConfigureRedaction(network.ChainID, key); err != nil {
		return err
	}
	genesis := make(map[protocol.OutputID]int, len(network.Genesis.Outputs))
	for i, origin := range network.Genesis.Outputs {
		genesis[origin.ID] = i
	}
	dir := filepath.Join(lab, "committee0/comet/data")
	blockDB, err := dbm.NewGoLevelDBWithOpts("blockstore", dir, &opt.Options{ReadOnly: true})
	if err != nil {
		return err
	}
	defer blockDB.Close()
	stateDB, err := dbm.NewGoLevelDBWithOpts("state", dir, &opt.Options{ReadOnly: true})
	if err != nil {
		return err
	}
	defer stateDB.Close()
	blocks := store.NewBlockStore(blockDB)
	states := cstate.NewStore(stateDB, cstate.StoreOptions{})
	latest, err := states.Load()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	seen := map[string]bool{}
	used := map[protocol.OutputID]bool{}
	counts := map[string]int{"exported": 0, "failed_execution": 0, "other_or_dependent": 0, "duplicate": 0}
	for height := blocks.Base(); height <= min(blocks.Height(), latest.LastBlockHeight); height++ {
		block := blocks.LoadBlock(height)
		if block == nil {
			return fmt.Errorf("missing block %d", height)
		}
		result, err := states.LoadFinalizeBlockResponse(height)
		if err != nil {
			return fmt.Errorf("finalize height %d: %w", height, err)
		}
		if len(result.TxResults) != len(block.Txs) {
			return fmt.Errorf("result length mismatch at %d", height)
		}
		for index, raw := range block.Txs {
			if result.TxResults[index] == nil || result.TxResults[index].Code != 0 {
				counts["failed_execution"]++
				continue
			}
			payment, err := decodePayment(raw)
			if err != nil {
				counts["other_or_dependent"]++
				continue
			}
			fact := protocol.Hash(payment.Authorization.Fact).String()
			if seen[fact] {
				counts["duplicate"]++
				continue
			}
			body := payment.Tx.Body
			independent := body.Kind == protocol.FastTransfer && body.Fee.Source == protocol.OrgReserve && len(body.Fee.Inputs) == 0 && len(payment.InputCertificates) == 0 && len(body.Inputs) > 0 && len(body.Inputs) == len(payment.Tx.Claims)
			localInputs := map[protocol.OutputID]bool{}
			for i, input := range body.Inputs {
				position, found := genesis[input.Output]
				if !independent || !found || input.Kind != protocol.FinalInput || used[input.Output] || localInputs[input.Output] {
					independent = false
					break
				}
				origin := network.Genesis.Outputs[position]
				claim := payment.Tx.Claims[i]
				if claim.Instance != 0 || claim.Output != origin.Output || input.Evidence != origin.Fact {
					independent = false
					break
				}
				localInputs[input.Output] = true
			}
			if !independent {
				counts["other_or_dependent"]++
				continue
			}
			if err = encoder.Encode(fixtureRow{Raw: raw, Fact: fact, Height: height}); err != nil {
				return err
			}
			seen[fact] = true
			for input := range localInputs {
				used[input] = true
			}
			counts["exported"]++
		}
	}
	if err = file.Sync(); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(counts)
}

type sendSample struct {
	Index     int    `json:"index"`
	Fact      string `json:"fact"`
	Scheduled int64  `json:"scheduled_unix_ns"`
	Sent      int64  `json:"sent_unix_ns"`
	Returned  int64  `json:"returned_unix_ns"`
	Status    int    `json:"status"`
	Error     string `json:"error,omitempty"`
	Endpoint  string `json:"endpoint"`
}

func sendFixture(input, endpoint string, count int, rate float64, output string) error {
	if count <= 0 || rate < 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return fmt.Errorf("invalid count or rate")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		return fmt.Errorf("output must not already exist: %s", output)
	}
	endpoints := strings.Split(endpoint, ",")
	for i, value := range endpoints {
		parsed, err := url.Parse(value)
		if err != nil {
			return err
		}
		if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("URL must be http(s)")
		}
		if parsed.Path == "" || parsed.Path == "/" {
			parsed.Path = "/v1/commands"
		}
		endpoints[i] = parsed.String()
	}
	file, err := os.Open(input)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(file)
	fixtures := make([]fixtureRow, count)
	seen := map[string]bool{}
	for i := range fixtures {
		if err = decoder.Decode(&fixtures[i]); err != nil {
			file.Close()
			return fmt.Errorf("fixture row %d: %w", i, err)
		}
		row := fixtures[i]
		payment, err := decodePayment(row.Raw)
		if err != nil || row.Fact != protocol.Hash(payment.Authorization.Fact).String() || seen[row.Fact] {
			file.Close()
			return fmt.Errorf("invalid or repeated fixture row %d", i)
		}
		seen[row.Fact] = true
	}
	if err = file.Close(); err != nil {
		return err
	}
	// Preload fixture bytes only. There are no warm-up requests or cache priming.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns, tr.MaxIdleConnsPerHost, tr.MaxConnsPerHost = 128, 128, 128
	client := &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	samples := make([]sendSample, count)
	permits := make(chan struct{}, 128)
	var wait sync.WaitGroup
	start := time.Now()
	for i, row := range fixtures {
		var scheduled time.Time
		if rate > 0 {
			scheduled = start.Add(time.Duration(float64(i) / rate * float64(time.Second)))
			if delay := time.Until(scheduled); delay > 0 {
				time.Sleep(delay)
			}
		}
		permits <- struct{}{}
		wait.Add(1)
		go func(i int, row fixtureRow, scheduled time.Time) {
			defer wait.Done()
			defer func() { <-permits }()
			s := &samples[i]
			s.Index, s.Fact = i, row.Fact
			if !scheduled.IsZero() {
				s.Scheduled = scheduled.UnixNano()
			}
			s.Endpoint = endpoints[i%len(endpoints)]
			if os.Getenv("COMMITTEE_ROUTE") == "hash" && len(endpoints) > 1 {
				digest := protocol.Digest("COMMITTEE_SUBMIT_ROUTE", row.Raw)
				s.Endpoint = endpoints[binary.BigEndian.Uint64(digest[:8])%uint64(len(endpoints))]
			}
			req, err := http.NewRequest(http.MethodPost, s.Endpoint, bytes.NewReader(row.Raw))
			if err != nil {
				s.Error = err.Error()
				s.Returned = time.Now().UnixNano()
				return
			}
			req.Header.Set("Content-Type", transport.MediaType)
			s.Sent = time.Now().UnixNano()
			response, err := client.Do(req)
			if err != nil {
				s.Error = err.Error()
				s.Returned = time.Now().UnixNano()
				return
			}
			s.Status = response.StatusCode
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
			closeErr := response.Body.Close()
			s.Returned = time.Now().UnixNano()
			if readErr != nil {
				s.Error = readErr.Error()
			} else if closeErr != nil {
				s.Error = closeErr.Error()
			}
			if s.Status != http.StatusAccepted {
				s.Error = fmt.Sprintf("HTTP %d: %s", s.Status, strings.TrimSpace(string(body)))
			}
		}(i, row, scheduled)
	}
	wait.Wait()
	finish := time.Now()
	accepted, failed := 0, 0
	var latencies, lags []float64
	firstSent, lastSent := int64(0), int64(0)
	for _, s := range samples {
		if s.Status == http.StatusAccepted && s.Error == "" {
			accepted++
		} else {
			failed++
		}
		if s.Sent == 0 {
			continue
		}
		if firstSent == 0 || s.Sent < firstSent {
			firstSent = s.Sent
		}
		lastSent = max(lastSent, s.Sent)
		latencies = append(latencies, float64(s.Returned-s.Sent)/1e6)
		if s.Scheduled > 0 {
			lags = append(lags, float64(s.Sent-s.Scheduled)/1e6)
		}
	}
	quantile := func(values []float64, q float64) any {
		if len(values) == 0 {
			return nil
		}
		sort.Float64s(values)
		return values[int(float64(len(values)-1)*q)]
	}
	summary := map[string]any{"count": count, "target_rate": rate, "max_in_flight": 128, "accepted": accepted, "failed": failed, "started_unix_ns": start.UnixNano(), "finished_unix_ns": finish.UnixNano(), "elapsed_s": finish.Sub(start).Seconds(), "admissions_per_second": float64(accepted) / finish.Sub(start).Seconds(), "http_p50_ms": quantile(latencies, .5), "http_p95_ms": quantile(latencies, .95), "dispatch_lag_p50_ms": quantile(lags, .5), "dispatch_lag_p95_ms": quantile(lags, .95), "actual_send_span_s": float64(lastSent-firstSent) / 1e9, "note": "HTTP 202 is mempool admission, not committed execution. Sent timestamp is immediately before HTTP Do. rate=0 has no planned schedule."}
	if lastSent > firstSent {
		summary["actual_send_rate"] = float64(len(latencies)-1) * 1e9 / float64(lastSent-firstSent)
	}
	out, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	encodedErr := json.NewEncoder(out).Encode(struct {
		Summary map[string]any
		Samples []sendSample
	}{summary, samples})
	closeErr := out.Close()
	if encodedErr != nil {
		return encodedErr
	}
	if closeErr != nil {
		return closeErr
	}
	return json.NewEncoder(os.Stdout).Encode(summary)
}
