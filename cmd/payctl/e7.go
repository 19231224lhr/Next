package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	cfg "utxo/cmd/internal/config"
	"utxo/protocol"
)

// E7 keeps the genesis pool identical across same/cross-organization cases.
func e7Command(args []string) error {
	f := flag.NewFlagSet("e7", flag.ContinueOnError)
	dir := f.String("dir", "", "fresh lab")
	capacity := f.Int("capacity", 65000, "genesis input capacity")
	audit := f.Bool("audit", false, "audit stopped E7 experiment")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *dir == "" || *capacity < 1 || *capacity > 1000000 {
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
	if len(n.Organizations) != 2 {
		return fmt.Errorf("E7 requires two organizations")
	}
	if *audit {
		return auditE7(*dir, lab, n)
	}
	if e := prepareE8(*dir, lab, n, e8Options{Wallets: 64, Capacity: *capacity, Seed: 23}); e != nil {
		return e
	}
	if e := cfg.Read(lab.Network, &n); e != nil {
		return e
	}
	ds := make([]protocol.ReceiveDescriptor, 64)
	for i := range ds {
		key, e := cfg.PrivateKey(filepath.Join(*dir, "keys", "e8", fmt.Sprintf("%d.key", i)))
		if e != nil {
			return e
		}
		ds[i] = protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: n.Organizations[i%2].Org}, key)
	}
	for i := range n.Genesis.Outputs {
		n.Genesis.Outputs[i].Output.Recipient = ds[(i/2)%64]
	}
	raw, e := json.Marshal(n)
	if e != nil {
		return e
	}
	return os.WriteFile(lab.Network, raw, 0600)
}
