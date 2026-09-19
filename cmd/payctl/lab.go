package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"flag"
	"fmt"
	cmtcfg "github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/p2p"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/crypto/chameleon"
	"utxo/internal/committee"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

type nodeSpec = cfg.NodeSpec
type labConfig = cfg.Lab

func initialize(args []string) error {
	flags := flag.NewFlagSet("init-lab", flag.ContinueOnError)
	dir := flags.String("dir", "", "new laboratory directory")
	base := flags.Int("port", 18000, "base loopback port")
	outputs := flags.Int("outputs", 1024, "finite initial CAL outputs per owner")
	direct := flags.Bool("v4", false, "new direct-liability genesis with a 3-of-4 repair key")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if *dir == "" || *outputs < 1 || *outputs > 100000 || *base < 1024 || *base > 65000-301 {
		return fmt.Errorf("invalid laboratory parameters")
	}
	root, e := filepath.Abs(*dir)
	if e != nil {
		return e
	}
	if e = os.Mkdir(root, 0700); e != nil {
		return e
	}
	for _, name := range []string{"keys", "config", "logs", "reports"} {
		if e = os.Mkdir(filepath.Join(root, name), 0700); e != nil {
			return e
		}
	}
	lab := labConfig{Network: filepath.Join(root, "config", "network.json")}
	var entropy [16]byte
	if _, e = rand.Read(entropy[:]); e != nil {
		return e
	}
	network := cfg.Network{ChainID: fmt.Sprintf("utxo-lab-%x", entropy[:8]), GenesisTime: time.Now().Add(-time.Second).UTC(), Schedule: rules.DefaultSchedule(), Members: make(map[protocol.Hash][4]string)}
	network.Genesis.Network = protocol.Digest("NETWORK", []byte(network.ChainID))
	var repairFiles [4]string
	if *direct {
		public, shares, err := chameleon.GenerateDealer()
		if err != nil {
			return err
		}
		network.Direct = &rules.DirectSettings{Modulus: public.Modulus(), TimeoutSeconds: 30, RepairCost: 5}
		for i, b := range shares {
			repairFiles[i] = filepath.Join(root, "keys", fmt.Sprintf("committee%d-repair.key", i))
			if err = os.WriteFile(repairFiles[i], b, 0600); err != nil {
				return err
			}
		}
	}
	keyFile := func(name string) (string, ed25519.PrivateKey, error) {
		_, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return "", nil, e
		}
		path := filepath.Join(root, "keys", name)
		e = os.WriteFile(path, key, 0600)
		return path, key, e
	}
	for orgIndex := 0; orgIndex < 2; orgIndex++ {
		org := protocol.OrgConfig{Network: network.Genesis.Network, Org: protocol.Digest("LAB_ORG", entropy[:], []byte{byte(orgIndex)}), Epoch: 1}
		var files [4]string
		var urls [4]string
		for i := 0; i < 4; i++ {
			path, key, e := keyFile(fmt.Sprintf("org%d-member%d.key", orgIndex, i))
			if e != nil {
				return e
			}
			files[i] = path
			copy(org.Members[i][:], key[32:])
			urls[i] = fmt.Sprintf("http://127.0.0.1:%d", *base+orgIndex*4+i)
		}
		ownerFile, owner, e := keyFile(fmt.Sprintf("owner%d.key", orgIndex))
		if e != nil {
			return e
		}
		lab.Owners[orgIndex] = ownerFile
		descriptor := protocol.NewDescriptor(network.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: org.Org}, owner)
		for i := 0; i < *outputs; i++ {
			id := protocol.OutputID(protocol.Digest("GENESIS_OUTPUT", org.Org[:], []byte(fmt.Sprint(i))))
			network.Genesis.Outputs = append(network.Genesis.Outputs, state.OriginOutput{ID: id, Output: protocol.Output{Asset: protocol.AssetCAL, Amount: 100, Recipient: descriptor}, Fact: protocol.Digest("GENESIS_OUTPUT_FACT", id[:])})
		}
		kinds := []protocol.ResourceKind{protocol.ResourceFUEL, protocol.ResourceExecution, protocol.ResourceBytes, protocol.ResourcePolicy}
		if *direct {
			kinds = append([]protocol.ResourceKind{protocol.ResourceCAL}, kinds...)
		}
		for _, kind := range kinds {
			account := org.Org
			if kind == protocol.ResourcePolicy {
				account = protocol.Digest("POLICY", org.Org[:], descriptor.Owner[:])
			}
			network.Genesis.Grants = append(network.Genesis.Grants, state.Grant{ID: protocol.Digest("GENESIS_GRANT", org.Org[:], []byte{byte(kind)}), Organization: org.Hash(), Key: protocol.ResourceKey{Kind: kind, Account: account, Version: 1}, Amount: 1_000_000_000_000, Subject: descriptor.Owner})
		}
		network.Accounts = append(network.Accounts, committee.GenesisAccount{Owner: org.Org, Asset: protocol.AssetFUEL, Balance: 1_000_000_000_000})
		if *direct {
			network.Accounts = append(network.Accounts, committee.GenesisAccount{Owner: org.Org, Asset: protocol.AssetCAL, Balance: 1000000000000})
		}
		network.Organizations = append(network.Organizations, org)
		network.Members[org.Org] = urls
		for i := 0; i < 4; i++ {
			name := fmt.Sprintf("org%d-member%d", orgIndex, i)
			file := filepath.Join(root, "config", name+".json")
			c := map[string]any{"Network": lab.Network, "DataDir": filepath.Join(root, name), "KeyFile": files[i], "Listen": strings.TrimPrefix(urls[i], "http://"), "Organization": org.Org, "Index": i, "Workers": 4}
			if e = cfg.Write(file, c); e != nil {
				return e
			}
			lab.Nodes = append(lab.Nodes, nodeSpec{Name: name, Binary: "member", Config: file, URL: urls[i]})
		}
		name := fmt.Sprintf("gateway%d", orgIndex)
		file := filepath.Join(root, "config", name+".json")
		url := fmt.Sprintf("http://127.0.0.1:%d", *base+300+orgIndex)
		lab.Gateways[orgIndex] = url
		c := map[string]any{"Network": lab.Network, "DataDir": filepath.Join(root, name), "Listen": strings.TrimPrefix(url, "http://"), "Organization": org.Org, "Members": urls}
		if e = cfg.Write(file, c); e != nil {
			return e
		}
		lab.Nodes = append(lab.Nodes, nodeSpec{Name: name, Binary: "gateway", Config: file, URL: url})
	}
	var committeeFiles [4]string
	var peers [4]string
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("committee%d", i)
		path, key, e := keyFile(name + ".key")
		if e != nil {
			return e
		}
		committeeFiles[i] = path
		copy(network.Committee[i][:], key[32:])
		cc := cmtcfg.DefaultConfig().SetRoot(filepath.Join(root, name, "comet"))
		cmtcfg.EnsureRoot(cc.RootDir)
		nk, e := p2p.LoadOrGenNodeKey(cc.NodeKeyFile())
		if e != nil {
			return e
		}
		peers[i] = fmt.Sprintf("%s@127.0.0.1:%d", nk.ID(), *base+200+i)
		network.CommitteeURLs[i] = fmt.Sprintf("http://127.0.0.1:%d", *base+100+i)
	}
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("committee%d", i)
		file := filepath.Join(root, "config", name+".json")
		var others []string
		for j, p := range peers {
			if i != j {
				others = append(others, p)
			}
		}
		c := map[string]any{"Network": lab.Network, "DataDir": filepath.Join(root, name), "KeyFile": committeeFiles[i], "P2PListen": fmt.Sprintf("tcp://127.0.0.1:%d", *base+200+i), "Peers": strings.Join(others, ","), "Listen": strings.TrimPrefix(network.CommitteeURLs[i], "http://"), "Index": i}
		if *direct {
			c["RepairKeyFile"] = repairFiles[i]
		}
		if e = cfg.Write(file, c); e != nil {
			return e
		}
		lab.Nodes = append(lab.Nodes, nodeSpec{Name: name, Binary: "committee", Config: file, URL: network.CommitteeURLs[i]})
	}
	if e = cfg.Write(lab.Network, network); e != nil {
		return e
	}
	if e = cfg.Write(filepath.Join(root, "lab.json"), lab); e != nil {
		return e
	}
	fmt.Printf("Created finite two-organization laboratory: %s\n", root)
	return nil
}
func runLab(args []string) error {
	flags := flag.NewFlagSet("lab-run", flag.ContinueOnError)
	dir := flags.String("dir", "", "laboratory directory")
	bin := flags.String("bin", "", "binary directory")
	if e := flags.Parse(args); e != nil {
		return e
	}
	var lab labConfig
	if e := cfg.Read(filepath.Join(*dir, "lab.json"), &lab); e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	type exit struct {
		name string
		err  error
	}
	exits := make(chan exit, len(lab.Nodes))
	var children []*exec.Cmd
	var logs []*os.File
	defer func() {
		for _, child := range children {
			_ = child.Process.Signal(os.Interrupt)
		}
		deadline := time.NewTimer(25 * time.Second)
		defer deadline.Stop()
		for range children {
			select {
			case <-exits:
			case <-deadline.C:
				for _, child := range children {
					_ = child.Process.Kill()
				}
				return
			}
		}
		for _, file := range logs {
			_ = file.Close()
		}
	}()
	for _, spec := range lab.Nodes {
		file, e := os.OpenFile(filepath.Join(*dir, "logs", spec.Name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			return e
		}
		logs = append(logs, file)
		child := exec.Command(filepath.Join(*bin, spec.Binary), "-config", spec.Config)
		child.Stdout = file
		child.Stderr = file
		if e = child.Start(); e != nil {
			return e
		}
		children = append(children, child)
		go func(name string, child *exec.Cmd) { exits <- exit{name, child.Wait()} }(spec.Name, child)
	}
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for _, spec := range lab.Nodes {
		ready := false
		for time.Now().Before(deadline) && ctx.Err() == nil {
			response, e := client.Get(spec.URL + "/healthz")
			if e == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					ready = true
					break
				}
			}
			select {
			case failed := <-exits:
				exits <- failed
				return fmt.Errorf("%s exited: %v", failed.name, failed.err)
			case <-time.After(100 * time.Millisecond):
			}
		}
		if !ready {
			return fmt.Errorf("%s did not become healthy", spec.Name)
		}
	}
	fmt.Printf("Laboratory running: %d independent processes; %s\n", len(children), *dir)
	select {
	case <-ctx.Done():
		return nil
	case failed := <-exits:
		exits <- failed
		return fmt.Errorf("%s exited: %v", failed.name, failed.err)
	}
}
