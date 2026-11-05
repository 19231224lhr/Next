package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"utxo/internal/poollab"
)

func main() {
	n := flag.Int("transfers", 100, "number of sequential transfers between three wallet owners")
	dir := flag.String("store-dir", "", "fresh synchronous Bolt store directory; empty uses memory")
	ordinary := flag.Bool("ordinary", false, "run ordinary certificate-chain baseline without public settlement")
	serial := flag.Bool("serial-completion", false, "serialize wallet receipt and member installation with the same readiness rule")
	flag.Parse()
	var r any
	var err error
	if *ordinary {
		r, err = poollab.RunOrdinary(context.Background(), *n, *dir)
	} else if *serial {
		r, err = poollab.RunSerial(context.Background(), *n, *dir)
	} else {
		r, err = poollab.Run(context.Background(), *n, *dir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err = enc.Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
