// lnbench measures real LND RPCs using one monotonic clock and persistent connections.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/routerrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

type nodeConfig struct{ Address, Directory string }
type node struct {
	conn   *grpc.ClientConn
	rpc    lnrpc.LightningClient
	router routerrpc.RouterClient
	ctx    context.Context
}

func connect(c nodeConfig) (*node, error) {
	tls, e := credentials.NewClientTLSFromFile(filepath.Join(c.Directory, "tls.cert"), "")
	if e != nil {
		return nil, e
	}
	mac, e := os.ReadFile(filepath.Join(c.Directory, "data/chain/bitcoin/regtest/admin.macaroon"))
	if e != nil {
		return nil, e
	}
	conn, e := grpc.NewClient(c.Address, grpc.WithTransportCredentials(tls))
	if e != nil {
		return nil, e
	}
	return &node{conn, lnrpc.NewLightningClient(conn), routerrpc.NewRouterClient(conn), metadata.AppendToOutgoingContext(context.Background(), "macaroon", hex.EncodeToString(mac))}, nil
}
func save(path string, v any) {
	b, e := json.MarshalIndent(v, "", "  ")
	must(e)
	must(os.WriteFile(path, b, 0644))
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
func main() {
	config := flag.String("config", "", "JSON array of two endpoint nodes")
	out := flag.String("out", "", "output directory")
	count := flag.Int("count", 100, "logical payments")
	rate := flag.Float64("rate", 0, "aggregate payments/s; zero means sequential alternating")
	duration := flag.Duration("duration", 0, "fixed open-loop send window; count=rate*duration")
	amount := flag.Int64("amount", 10000, "satoshis per payment")
	chain := flag.Bool("chain", false, "verify receiver lacks q before each serial payment")
	direction := flag.String("direction", "balanced", "balanced or forward (node 0 to node 1)")
	diagnostic := flag.Bool("diagnostic", false, "retain every payment status update for retry inspection")
	limit := flag.Int("concurrency", 1024, "maximum outstanding payment RPCs")
	timeout := flag.Duration("timeout", 30*time.Second, "per payment deadline")
	flag.Parse()
	if *direction != "balanced" && *direction != "forward" {
		panic("invalid direction")
	}
	if *chain && *rate != 0 {
		panic("chain requires serial rate=0")
	}
	if *config == "" || *out == "" || *count < 1 || *limit < 1 {
		panic("config/out/count/concurrency required")
	}
	if *duration > 0 {
		if *rate <= 0 {
			panic("duration requires positive rate")
		}
		*count = int(*rate * duration.Seconds())
	}
	must(os.MkdirAll(*out, 0755))
	var cfg []nodeConfig
	b, e := os.ReadFile(*config)
	must(e)
	must(json.Unmarshal(b, &cfg))
	if len(cfg) != 2 {
		panic("exactly two endpoints")
	}
	nodes := make([]*node, 2)
	for i := range nodes {
		nodes[i], e = connect(cfg[i])
		must(e)
		defer nodes[i].conn.Close()
	}
	audit := func(name string) {
		var snapshots []any
		for _, n := range nodes {
			ctx, cancel := context.WithTimeout(n.ctx, 10*time.Second)
			ch, e := n.rpc.ListChannels(ctx, &lnrpc.ListChannelsRequest{})
			must(e)
			bal, e := n.rpc.ChannelBalance(ctx, &lnrpc.ChannelBalanceRequest{})
			must(e)
			info, e := n.rpc.GetInfo(ctx, &lnrpc.GetInfoRequest{})
			must(e)
			cancel()
			snapshots = append(snapshots, map[string]any{"channels": ch, "balance": bal, "info": info})
		}
		save(filepath.Join(*out, name), snapshots)
	}
	audit("before.json")
	rows := make([]sample, *count)
	updates := make([][]*lnrpc.Payment, *count)
	requests := make([]string, *count)
	byHash := make(map[string]int, *count)
	// Invoice preparation is outside the measured interval, just like funded input preparation.
	for i := range rows {
		sender := i % 2
		if *direction == "forward" {
			sender = 0
		}
		ctx, cancel := context.WithTimeout(nodes[1-sender].ctx, 10*time.Second)
		inv, e := nodes[1-sender].rpc.AddInvoice(ctx, &lnrpc.Invoice{Value: *amount, Expiry: 86400, Memo: fmt.Sprintf("lnbench-%d", i)})
		cancel()
		must(e)
		h := hex.EncodeToString(inv.RHash)
		rows[i] = sample{Index: i, Sender: sender, Hash: h, Status: "NOT_SENT"}
		requests[i] = inv.PaymentRequest
		byHash[h] = i
	}
	fmt.Printf("prepared %d invoices\n", len(rows))
	var mu sync.Mutex
	origin := time.Now()
	var observers sync.WaitGroup
	var cancels []context.CancelFunc
	for _, n := range nodes {
		ctx, cancel := context.WithCancel(n.ctx)
		cancels = append(cancels, cancel)
		stream, e := n.rpc.SubscribeInvoices(ctx, &lnrpc.InvoiceSubscription{})
		must(e)
		observers.Add(1)
		go func() {
			defer observers.Done()
			for {
				v, e := stream.Recv()
				if e != nil {
					if ctx.Err() == nil {
						fmt.Fprintln(os.Stderr, "invoice subscription:", e)
					}
					return
				}
				if v.State != lnrpc.Invoice_SETTLED {
					continue
				}
				at := time.Since(origin).Nanoseconds()
				mu.Lock()
				if i, ok := byHash[hex.EncodeToString(v.RHash)]; ok && rows[i].Settled == 0 {
					rows[i].Settled = at
				}
				mu.Unlock()
			}
		}()
	}
	// A barrier RPC ensures both streams are created before the first payment.
	for _, n := range nodes {
		ctx, cancel := context.WithTimeout(n.ctx, 10*time.Second)
		_, e = n.rpc.GetInfo(ctx, &lnrpc.GetInfoRequest{})
		cancel()
		must(e)
	}
	start := time.Now()
	offset := start.Sub(origin).Nanoseconds()
	var wg sync.WaitGroup
	slots := make(chan struct{}, *limit)
	progress := make([]map[string]any, 0)
	stopSample := make(chan struct{})
	sampleDone := make(chan struct{})
	go func() {
		defer close(sampleDone)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stopSample:
				return
			case <-tick.C:
				mu.Lock()
				sent, done, ok := 0, 0, 0
				oldest := int64(0)
				now := time.Since(origin).Nanoseconds()
				for _, r := range rows {
					if r.Sent > 0 {
						sent++
					}
					if r.Done > 0 {
						done++
					}
					if r.Status == "SUCCEEDED" {
						ok++
					}
					if r.Sent > 0 && r.Done == 0 && now-r.Sent > oldest {
						oldest = now - r.Sent
					}
				}
				progress = append(progress, map[string]any{"seconds": time.Since(start).Seconds(), "sent": sent, "done": done, "succeeded": ok, "pending": sent - done, "oldest_ms": float64(oldest) / 1e6})
				mu.Unlock()
			}
		}
	}()
	pay := func(i int) {
		defer wg.Done()
		defer func() { <-slots }()
		n := nodes[rows[i].Sender]
		ctx, cancel := context.WithTimeout(n.ctx, *timeout)
		defer cancel()
		mu.Lock()
		now := time.Now()
		if *duration > 0 && !now.Before(start.Add(*duration)) {
			mu.Unlock()
			return
		}
		rows[i].Sent = now.Sub(origin).Nanoseconds()
		rows[i].Status = "IN_FLIGHT"
		mu.Unlock()
		stream, e := n.router.SendPaymentV2(ctx, &routerrpc.SendPaymentRequest{PaymentRequest: requests[i], TimeoutSeconds: int32(timeout.Seconds()), FeeLimitSat: 100, MaxParts: 1, NoInflightUpdates: !*diagnostic})
		var terminal *lnrpc.Payment
		if e == nil {
			for {
				p, err := stream.Recv()
				if err != nil {
					e = err
					break
				}
				updates[i] = append(updates[i], p)
				if p.Status == lnrpc.Payment_SUCCEEDED || p.Status == lnrpc.Payment_FAILED {
					terminal = p
					break
				}
			}
		}
		at := time.Since(origin).Nanoseconds()
		mu.Lock()
		defer mu.Unlock()
		r := &rows[i]
		r.Done = at
		if terminal != nil {
			r.Status = terminal.Status.String()
			r.FeeMSat = terminal.FeeMsat
			if terminal.Status == lnrpc.Payment_SUCCEEDED {
				r.Success = at
			} else {
				r.Error = terminal.FailureReason.String()
			}
		} else {
			r.Status = "UNKNOWN"
			r.Error = fmt.Sprint(e)
		}
	}
	var chainChecks []map[string]any
	for i := range rows {
		if *chain {
			checkStart := time.Now()
			check := map[string]any{"index": i, "receiver": 1 - rows[i].Sender}
			var ch *lnrpc.ListChannelsResponse
			deadline := time.Now().Add(10 * time.Second)
			for {
				ctx, cancel := context.WithTimeout(nodes[1-rows[i].Sender].ctx, 5*time.Second)
				ch, e = nodes[1-rows[i].Sender].rpc.ListChannels(ctx, &lnrpc.ListChannelsRequest{})
				cancel()
				must(e)
				pending := 0
				for _, c := range ch.Channels {
					pending += len(c.PendingHtlcs)
				}
				if pending == 0 || time.Now().After(deadline) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			valid := len(ch.Channels) == 1 && len(ch.Channels[0].PendingHtlcs) == 0 && ch.Channels[0].LocalBalance < *amount
			check["channels"] = ch
			check["query_wait_ns"] = time.Since(checkStart).Nanoseconds()
			check["valid"] = valid
			chainChecks = append(chainChecks, check)
			if !valid {
				fmt.Fprintln(os.Stderr, "chain precondition failed at", i)
				break
			}
		}
		planned := time.Now()
		if *rate > 0 {
			planned = start.Add(time.Duration(float64(i) * 1e9 / *rate))
		}
		mu.Lock()
		rows[i].Planned = planned.Sub(origin).Nanoseconds()
		mu.Unlock()
		if wait := time.Until(planned); wait > 0 {
			time.Sleep(wait)
		}
		if *duration > 0 {
			remaining := time.Until(start.Add(*duration))
			if remaining <= 0 {
				break
			}
			timer := time.NewTimer(remaining)
			select {
			case slots <- struct{}{}:
				timer.Stop()
			case <-timer.C:
				break
			}
			if time.Since(start) >= *duration {
				break
			}
		} else {
			slots <- struct{}{}
		}
		wg.Add(1)
		go pay(i)
		if *rate == 0 {
			wg.Wait()
			if *chain && rows[i].Status != "SUCCEEDED" {
				break
			}
		}
	}
	wg.Wait()
	elapsed := time.Since(start).Nanoseconds()
	// Allow delayed subscription delivery, but do not replace missing event timestamps with queries.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		missing := false
		mu.Lock()
		for _, r := range rows {
			if r.Status == "SUCCEEDED" && r.Settled == 0 {
				missing = true
				break
			}
		}
		mu.Unlock()
		if !missing {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(stopSample)
	<-sampleDone
	for _, cancel := range cancels {
		cancel()
	}
	observers.Wait()
	save(filepath.Join(*out, "payments.json"), rows)
	save(filepath.Join(*out, "payment-updates.json"), updates)
	save(filepath.Join(*out, "progress.json"), progress)
	save(filepath.Join(*out, "chain-checks.json"), chainChecks)
	s := summarize(rows, elapsed)
	save(filepath.Join(*out, "summary.json"), s)
	// Lookup is an independent reconciliation, never a measured receive timestamp.
	var reconciliation []any
	for i, r := range rows {
		if r.Sent == 0 {
			continue
		}
		hash, _ := hex.DecodeString(r.Hash)
		ctx, cancel := context.WithTimeout(nodes[1-r.Sender].ctx, 10*time.Second)
		v, e := nodes[1-r.Sender].rpc.LookupInvoice(ctx, &lnrpc.PaymentHash{RHash: hash})
		cancel()
		entry := map[string]any{"index": i, "hash": r.Hash}
		if e != nil {
			entry["error"] = e.Error()
		} else {
			entry["state"] = v.State.String()
			entry["paid_msat"] = v.AmtPaidMsat
		}
		reconciliation = append(reconciliation, entry)
	}
	save(filepath.Join(*out, "reconciliation.json"), reconciliation)
	save(filepath.Join(*out, "parameters.json"), map[string]any{"rate": *rate, "count": *count, "amount_sat": *amount, "concurrency": *limit, "duration_seconds": duration.Seconds(), "timeout_seconds": timeout.Seconds(), "start_offset_ns": offset, "clock": "one Go monotonic clock", "direction": *direction, "diagnostic": *diagnostic, "chain": *chain, "max_parts": 1})
	audit("after.json")
	b, _ = json.Marshal(s)
	fmt.Println(string(b))
}
