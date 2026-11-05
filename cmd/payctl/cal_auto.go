package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/budgetprobe"
	"utxo/internal/committee"
	"utxo/internal/reservecontrol"
	"utxo/internal/state"
	"utxo/internal/transport"
	"utxo/protocol"
)

// autoCALRunner owns all controller state in a background process. It never
// handles a wallet request and never changes a GM's quota directly.
type autoCALRunner struct {
	network    protocol.Hash
	grant      state.Grant
	key        ed25519.PrivateKey
	control    reservecontrol.Controller
	sample     func(context.Context) (reservecontrol.PublicState, [4]reservecontrol.MemberState, error)
	submit     func(context.Context, []byte) error
	log        io.Writer
	pending    []byte
	signed     reservecontrol.Increase
	lastSubmit time.Time
}

func autoCALMemberState(s budgetprobe.Snapshot, g state.Grant) (reservecontrol.MemberState, error) {
	for _, r := range s.Resources {
		if r.Key != g.Key {
			continue
		}
		if len(r.Slices) == 0 || len(r.Slices) > 256 {
			return reservecontrol.MemberState{}, protocol.ErrRule
		}
		m := reservecontrol.MemberState{Valid: true, Grant: r.Grant, ApprovedCAL: s.ApprovedCAL, LargestCAL: s.LargestCAL}
		for _, s := range r.Slices {
			var e error
			m.Available, e = protocol.Add(m.Available, s.Available)
			if e != nil {
				return reservecontrol.MemberState{}, e
			}
			m.Slices = append(m.Slices, s.Available)
		}
		return m, nil
	}
	return reservecontrol.MemberState{}, errors.New("member CAL grant missing")
}

func (r *autoCALRunner) tick(ctx context.Context, now time.Time) error {
	p, ms, e := r.sample(ctx)
	if e != nil {
		return e
	}
	inc, e := r.control.Step(now, p, ms)
	if e != nil {
		return e
	}
	event := "sample"
	if inc == nil {
		if r.pending != nil {
			event = "effective"
		}
		r.pending = nil
	} else {
		if r.pending == nil || r.signed != *inc {
			c := protocol.ReserveIncrease{Network: r.network, Organization: r.grant.Organization, Key: r.grant.Key, Grant: r.grant.ID, Previous: inc.Previous, Amount: inc.Amount}
			c.Sign(r.key)
			r.pending, e = c.MarshalBinary()
			if e != nil {
				return e
			}
			r.signed = *inc
			r.lastSubmit = time.Time{}
			event = "increase"
		}
		if r.lastSubmit.IsZero() || now.Sub(r.lastSubmit) >= time.Second {
			r.lastSubmit = now
			if e = r.submit(ctx, r.pending); e != nil {
				return e
			}
		}
	}
	if r.log != nil {
		return json.NewEncoder(r.log).Encode(map[string]any{"event": event, "at_ns": now.UnixNano(), "grant": p.Grant, "available": r.control.Available, "demand_cal": r.control.Demand, "lead_ms": r.control.Lead.Milliseconds(), "pending": r.pending != nil, "increase": inc})
	}
	return nil
}

func budgetSnapshot(ctx context.Context, client *http.Client, url string) (budgetprobe.Snapshot, error) {
	var s budgetprobe.Snapshot
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if e != nil {
		return s, e
	}
	res, e := client.Do(req)
	if e != nil {
		return s, e
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return s, fmt.Errorf("budget HTTP %d from %s", res.StatusCode, url)
	}
	const limit = 256 * 1024
	raw, e := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if e != nil {
		return s, e
	}
	if len(raw) > limit {
		return s, protocol.ErrEncoding
	}
	e = json.Unmarshal(raw, &s)
	return s, e
}

func sampleAutoCAL(ctx context.Context, client *http.Client, n cfg.Network, org protocol.OrgConfig, g state.Grant) (reservecontrol.PublicState, [4]reservecontrol.MemberState, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var p reservecontrol.PublicState
	var ms [4]reservecontrol.MemberState
	var publicErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s, e := budgetSnapshot(ctx, client, n.CommitteeURLs[0]+"/debug/budget/"+org.Org.String()+"?summary=1")
		if e != nil {
			publicErr = e
			return
		}
		for _, r := range s.Resources {
			if r.Key == g.Key {
				p = reservecontrol.PublicState{Grant: r.Grant, Reserved: r.Usage.Reserved, Spent: r.Usage.Spent}
				return
			}
		}
		publicErr = errors.New("public CAL grant missing")
	}()
	for i, url := range n.Members[org.Org] {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			s, e := budgetSnapshot(ctx, client, url+"/debug/budget")
			if e != nil {
				return
			}
			ms[i], _ = autoCALMemberState(s, g)
		}(i, url)
	}
	wg.Wait()
	return p, ms, publicErr
}

// Fresh experiments pre-fund the existing external account. No live balance,
// old approval, lock or protocol validation is modified by this preparation.
func prepareAutoCALFunding(n *cfg.Network, g state.Grant, amount uint64) error {
	owner := protocol.ReserveFundingAccount(n.Genesis.Network, g.Subject)
	for _, a := range n.Accounts {
		if a.Owner == owner && a.Asset == protocol.AssetCAL {
			return errors.New("CAL funding already initialized")
		}
	}
	n.Accounts = append(n.Accounts, committee.GenesisAccount{Owner: owner, Asset: protocol.AssetCAL, Balance: amount})
	return nil
}

func autoCALCommand(args []string) error {
	f := flag.NewFlagSet("reserve-auto", flag.ContinueOnError)
	dir := f.String("dir", "", "laboratory directory")
	index := f.Int("org", 0, "organization index in network configuration")
	keyPath := f.String("key", "", "CAL grant subject key; defaults to matching lab owner")
	prepare := f.Uint64("prepare-funding", 0, "seed external CAL account before starting a fresh lab, then exit")
	interval := f.Duration("interval", time.Second, "background check interval")
	lead := f.Duration("lead", time.Second, "initial submit-to-GM lead time; raised by observations")
	window := f.Duration("window", time.Minute, "historical demand window")
	minimum := f.Uint64("minimum", 100, "minimum forecast CAL headroom")
	duration := f.Duration("duration", 0, "optional run duration; zero waits for interruption")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *dir == "" || *index < 0 || *interval < 10*time.Millisecond || *lead <= 0 || *window <= 0 || *window / *interval > 4096 || *duration < 0 {
		return protocol.ErrRule
	}
	var lab cfg.Lab
	var n cfg.Network
	if e := cfg.Read(filepath.Join(*dir, "lab.json"), &lab); e != nil {
		return e
	}
	if e := cfg.Read(lab.Network, &n); e != nil {
		return e
	}
	if n.Direct == nil || *index >= len(n.Organizations) {
		return protocol.ErrRule
	}
	org := n.Organizations[*index]
	var g state.Grant
	for _, candidate := range n.Genesis.Grants {
		if candidate.Organization == org.Hash() && candidate.Key.Kind == protocol.ResourceCAL {
			if g.ID != (protocol.Hash{}) {
				return errors.New("multiple CAL grants: select one organization grant")
			}
			g = candidate
		}
	}
	if g.ID == (protocol.Hash{}) {
		return errors.New("CAL grant missing")
	}
	if *prepare != 0 {
		if e := prepareAutoCALFunding(&n, g, *prepare); e != nil {
			return e
		}
		return cfg.Write(lab.Network, n)
	}
	var key ed25519.PrivateKey
	paths := append([]string(nil), lab.Owners[:]...)
	if *keyPath != "" {
		paths = []string{*keyPath}
	}
	for _, path := range paths {
		k, e := cfg.PrivateKey(path)
		if e != nil {
			return e
		}
		if bytes.Equal(k.Public().(ed25519.PublicKey), g.Subject[:]) {
			key = k
			break
		}
	}
	if key == nil {
		return errors.New("CAL funding subject key not found; specify --key")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}
	client := transport.NewHTTPClient(2 * time.Second)
	public := transport.NewCommitteeClient(n.CommitteeURLs[0], n.CommitteeURLs[1:]...)
	r := autoCALRunner{network: n.Genesis.Network, grant: g, key: key, control: reservecontrol.Controller{Interval: *interval, Lead: *lead, Minimum: *minimum, History: reservecontrol.DemandHistory{Window: *window}}, sample: func(ctx context.Context) (reservecontrol.PublicState, [4]reservecontrol.MemberState, error) {
		return sampleAutoCAL(ctx, client, n, org, g)
	}, submit: public.Submit, log: os.Stdout}
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		if e := r.tick(ctx, time.Now()); e != nil && ctx.Err() == nil {
			if e = json.NewEncoder(os.Stdout).Encode(map[string]any{"event": "sample_or_submit_error", "error": e.Error()}); e != nil {
				return e
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
