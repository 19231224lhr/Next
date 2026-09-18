package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: payctl init-lab | lab-run | demo | audit | wallet-relay")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "init-lab":
		err = initialize(os.Args[2:])
	case "lab-run":
		err = runLab(os.Args[2:])
	case "audit":
		err = audit(os.Args[2:])
	case "wallet-relay":
		err = relayWallet(os.Args[2:])
	case "demo":
		err = demo(os.Args[2:])
	case "demo-v3":
		err = demoDirect(os.Args[2:])
	case "bench-v3":
		err = benchDirect(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
