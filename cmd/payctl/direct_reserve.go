package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/budgetprobe"
	"utxo/internal/committee"
	"utxo/internal/reservecontrol"
	"utxo/internal/transport"
	"utxo/protocol"
)

func prepareReserve(lab cfg.Lab, n cfg.Network, maximum uint64) error {
	for _, g := range n.Genesis.Grants {
		if g.Organization != n.Organizations[0].Hash() || g.Key.Kind != protocol.ResourceCAL {
			continue
		}
		if maximum <= g.Amount {
			return protocol.ErrRule
		}
		source := protocol.ReserveFundingAccount(n.Genesis.Network, g.Subject)
		for _, a := range n.Accounts {
			if a.Owner == source {
				return fmt.Errorf("reserve source already initialized")
			}
		}
		n.Accounts = append(n.Accounts, committee.GenesisAccount{Owner: source, Asset: protocol.AssetCAL, Balance: maximum - g.Amount})
		return cfg.Write(lab.Network, n)
	}
	return protocol.ErrRule
}

type reserveMonitor struct{ waiting sync.Map }

func (m *reserveMonitor) begin(id protocol.TxID) { m.waiting.Store(id, time.Now()) }
func (m *reserveMonitor) ready(id protocol.TxID) { m.waiting.Delete(id) }
func (m *reserveMonitor) pressure() (count uint64, oldest time.Duration) {
	m.waiting.Range(func(_, v any) bool { count++; oldest = max(oldest, time.Since(v.(time.Time))); return true })
	return
}

// The E2 offered trace is a declared demand envelope, independent of completed TPS.
// Each outstanding not-yet-certified request is conservatively added to Burst.
func (m *reserveMonitor) run(ctx context.Context, dir string, n cfg.Network, key ed25519.PrivateKey, policy reservecontrol.Policy, demand func() uint64) error {
	log, err := os.Create(filepath.Join(dir, "reports", "reserve-control.jsonl"))
	if err != nil {
		return err
	}
	defer log.Close()
	write := func(v any) { _ = json.NewEncoder(log).Encode(v) }
	org := n.Organizations[0]
	var grant protocol.ReserveIncrease
	for _, g := range n.Genesis.Grants {
		if g.Organization == org.Hash() && g.Key.Kind == protocol.ResourceCAL {
			grant = protocol.ReserveIncrease{Network: n.Genesis.Network, Organization: g.Organization, Key: g.Key, Grant: g.ID}
			break
		}
	}
	if grant.Grant == (protocol.Hash{}) {
		return protocol.ErrRule
	}
	public := transport.NewCommitteeClient(n.CommitteeURLs[0], n.CommitteeURLs[1:]...)
	client := transport.NewHTTPClient(2 * time.Second)
	var pending []byte
	var target uint64
	var began, lastSubmit time.Time
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		now := time.Now()
		var snapshots [4]budgetprobe.Snapshot
		var errors [4]error
		var wg sync.WaitGroup
		for i, url := range n.Members[org.Org] {
			wg.Add(1)
			go func(i int, url string) {
				defer wg.Done()
				req, e := http.NewRequestWithContext(ctx, "GET", url+"/debug/budget", nil)
				if e != nil {
					errors[i] = e
					return
				}
				res, e := client.Do(req)
				if e != nil {
					errors[i] = e
					return
				}
				defer res.Body.Close()
				if res.StatusCode != 200 {
					errors[i] = fmt.Errorf("budget HTTP %d", res.StatusCode)
					return
				}
				errors[i] = json.NewDecoder(res.Body).Decode(&snapshots[i])
			}(i, url)
		}
		wg.Wait()
		if ctx.Err() != nil {
			return nil
		}
		available, current := ^uint64(0), ^uint64(0)
		valid := true
		for i, s := range snapshots {
			if errors[i] != nil {
				valid = false
				write(map[string]any{"event": "sample_error", "at_ns": now.UnixNano(), "error": errors[i].Error()})
				continue
			}
			found := false
			for _, r := range s.Resources {
				if r.Key != grant.Key {
					continue
				}
				found = true
				var free uint64
				for _, slice := range r.Slices {
					free += slice.Available
				}
				available = min(available, free)
				current = min(current, r.Grant)
			}
			valid = valid && found
		}
		if valid {
			count, age := m.pressure()
			p := policy
			p.DemandPerSecond = demand()
			p.Burst += count * 100
			low, high, delta, e := p.Decide(current, available)
			if e != nil {
				return e
			}
			write(map[string]any{"event": "sample", "at_ns": now.UnixNano(), "grant": current, "available": available, "low": low, "target": high, "demand_cal_s": p.DemandPerSecond, "uncertified": count, "oldest_ms": age.Milliseconds(), "pending": pending != nil})
			if pending != nil && current >= target {
				write(map[string]any{"event": "effective", "at_ns": time.Now().UnixNano(), "total": target, "latency_ms": time.Since(began).Seconds() * 1000})
				pending = nil
			}
			if age > 30*time.Second {
				write(map[string]any{"event": "stopped_old_request", "at_ns": now.UnixNano()})
				return nil
			}
			if pending == nil && delta > 0 {
				grant.Previous = current
				grant.Amount = delta
				grant.Sign(key)
				pending, e = grant.MarshalBinary()
				if e != nil {
					return e
				}
				target = current + delta
				began = time.Now()
				lastSubmit = time.Time{}
				write(map[string]any{"event": "increase", "at_ns": began.UnixNano(), "previous": current, "delta": delta, "total": target})
			}
			if pending != nil && time.Since(lastSubmit) >= time.Second {
				if time.Since(began) > 10*time.Second {
					return fmt.Errorf("reserve increment did not reach all members in 10s")
				}
				lastSubmit = time.Now()
				e = public.Submit(ctx, pending)
				if e != nil && ctx.Err() == nil {
					write(map[string]any{"event": "submit_error", "at_ns": time.Now().UnixNano(), "error": e.Error()})
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
