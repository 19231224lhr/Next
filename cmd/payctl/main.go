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
	case "demo-v4":
		err = demoDirect(os.Args[2:])
	case "chain-v4":
		err = chainDirect(os.Args[2:])
	case "budget-v4":
		err = budgetDirect(os.Args[2:])
	case "liability-v4":
		err = liabilityDirect(os.Args[2:])
	case "budget-audit":
		err = auditBudget(os.Args[2:])
	case "bench-v4":
		err = benchDirect(os.Args[2:])
	case "fault-proxy":
		err = faultProxy(os.Args[2:])
	case "e5-proxy":
		err = e5ProxyCommand(os.Args[2:])
	case "e5-load":
		err = e5LoadCommand(os.Args[2:])
	case "fault-v4":
		err = faultDirect(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
