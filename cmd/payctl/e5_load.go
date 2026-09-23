package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/state"
	"utxo/protocol"
)

func prepareE5Chain(dir string, lab cfg.Lab, count int) error {
	var n cfg.Network
	if e := cfg.Read(lab.Network, &n); e != nil {
		return e
	}
	for user, path := range []string{lab.Owners[0], lab.Owners[1], filepath.Join(dir, "keys", "chain-c.key")} {
		key, e := cfg.PrivateKey(path)
		if e != nil {
			return e
		}
		d := protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: n.Organizations[0].Org}, key)
		fuelCount := count
		if user == 0 {
			fuelCount += 100
		}
		for i := 0; i < fuelCount; i++ {
			id := protocol.OutputID(protocol.Digest("E5_FUEL", []byte(fmt.Sprintf("%d/%d", user, i))))
			n.Genesis.Outputs = append(n.Genesis.Outputs, state.OriginOutput{ID: id, Fact: protocol.Digest("E5_FINAL", id[:]), Output: protocol.Output{Asset: protocol.AssetFUEL, Amount: 10000, Recipient: d}})
		}
		if user == 0 {
			for i := 0; i < 100; i++ {
				id := protocol.OutputID(protocol.Digest("E5_WARM_CAL", []byte(fmt.Sprint(i))))
				n.Genesis.Outputs = append(n.Genesis.Outputs, state.OriginOutput{ID: id, Fact: protocol.Digest("E5_FINAL", id[:]), Output: protocol.Output{Asset: protocol.AssetCAL, Amount: 100, Recipient: d}})
			}
		}
	}
	gs := n.Genesis.Grants[:0]
	for _, g := range n.Genesis.Grants {
		if g.Key.Kind != protocol.ResourceFUEL && g.Key.Kind != protocol.ResourcePolicy {
			gs = append(gs, g)
		}
	}
	n.Genesis.Grants = gs
	as := n.Accounts[:0]
	for _, a := range n.Accounts {
		if a.Asset != protocol.AssetFUEL {
			as = append(as, a)
		}
	}
	n.Accounts = as
	raw, e := json.Marshal(n)
	if e != nil {
		return e
	}
	return os.WriteFile(lab.Network, raw, 0600)
}

func e5LoadCommand(args []string) error {
	f := flag.NewFlagSet("e5-load", flag.ContinueOnError)
	dir := f.String("dir", "", "lab")
	count := f.Int("count", 100, "cohort")
	offset := f.Int("offset", 0, "skip warmup inputs")
	rate := f.Float64("rate", 200, "planned sends/s")
	window := f.Duration("window", 0, "fixed sending window")
	seed := f.Int64("seed", 23, "input permutation")
	prepare := f.Bool("prepare", false, "prepare user-paid genesis")
	audit := f.Bool("audit", false, "audit actual payments; explicitly unsent requests remain reported")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *count < 1 || *count+*offset > 30100 || *offset < 0 || *rate <= 0 {
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
	if *prepare {
		return prepareFault(lab, n, *count+*offset)
	}
	if *audit {
		return auditFault(*dir, lab, n, true)
	}
	return runFaultLoad(*dir, lab, n, *count, *rate, 60*time.Second, 30*time.Second, e5LoadOptions{*offset, *window, *seed})
}
