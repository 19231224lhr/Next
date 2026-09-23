package main

import (
	"flag"
	"path/filepath"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/protocol"
)

func e5LoadCommand(args []string) error {
	f := flag.NewFlagSet("e5-load", flag.ContinueOnError)
	dir := f.String("dir", "", "lab")
	count := f.Int("count", 100, "cohort")
	offset := f.Int("offset", 0, "skip warmup inputs")
	rate := f.Float64("rate", 200, "planned sends/s")
	window := f.Duration("window", 0, "fixed sending window")
	seed := f.Int64("seed", 23, "input permutation")
	prepare := f.Bool("prepare", false, "prepare user-paid genesis")
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
	return runFaultLoad(*dir, lab, n, *count, *rate, 60*time.Second, 30*time.Second, e5LoadOptions{*offset, *window, *seed})
}
