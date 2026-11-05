// poolctl exercises the opt-in, indivisible internal-funds protocol.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/gateway"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

func run() error {
	network := flag.String("network", "", "network JSON with EnableInternalPools=true")
	keyfile := flag.String("key", "", "owner private key file")
	dir := flag.String("wallet-dir", "", "durable wallet directory (reuse on retry)")
	action := flag.String("action", "", "PoolAction JSON; From/Signature are set locally")
	parent := flag.String("parent", "", "previous certificate binary, required for transfer/exit")
	output := flag.String("out", "", "certificate binary output")
	submit := flag.Bool("submit", false, "submit deposit/exit to public committee (acceptance is not finality)")
	receive := flag.String("receive", "", "verify and save internal transfer certificate instead of sending")
	flag.Parse()
	if *dir == "" || *network == "" || *keyfile == "" {
		return fmt.Errorf("network, key and wallet-dir are required")
	}
	var n cfg.Network
	if e := cfg.Read(*network, &n); e != nil {
		return e
	}
	if !n.EnableInternalPools {
		return protocol.ErrUnsupported
	}
	key, e := cfg.PrivateKey(*keyfile)
	if e != nil {
		return e
	}
	var owner protocol.PublicKey
	copy(owner[:], key[32:])
	if e = os.MkdirAll(*dir, 0700); e != nil {
		return e
	}
	db, e := store.Open(filepath.Join(*dir, "wallet.db"), store.Identity{Network: n.Genesis.Network.String(), Role: "pool-wallet", Node: fmt.Sprintf("%x", owner), Schema: 1})
	if e != nil {
		return e
	}
	defer db.Close()
	w, e := wallet.NewWithGenesis(db, n.Genesis.Network, owner, n.Organizations, n.Genesis)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if *receive != "" {
		b, e := os.ReadFile(*receive)
		if e != nil {
			return e
		}
		c, e := protocol.DecodePoolCertificate(b)
		if e != nil {
			return e
		}
		_, members, err := configuredPoolMembers(n, c.Action.Config)
		if err != nil {
			return err
		}
		return completePoolReceipt(ctx, w, c, members)
	}
	if *action == "" || *output == "" {
		return fmt.Errorf("action and out are required")
	}
	var r protocol.PoolRequest
	if e = cfg.Read(*action, &r.Action); e != nil {
		return e
	}
	r.Action.Sign(key)
	if *parent != "" {
		b, e := os.ReadFile(*parent)
		if e != nil {
			return e
		}
		p, e := protocol.DecodePoolCertificate(b)
		if e != nil {
			return e
		}
		r.Parent = &p
	}
	org, members, e := configuredPoolMembers(n, r.Action.Config)
	if e != nil {
		return e
	}
	if *submit && r.Action.Kind == protocol.PoolTransfer {
		return fmt.Errorf("internal transfer receipts cannot be submitted publicly")
	}
	if r.Parent != nil && r.Parent.Action.Kind == protocol.PoolTransfer {
		if e = completePoolReceipt(ctx, w, *r.Parent, members); e != nil {
			return fmt.Errorf("confirm parent availability: %w", e)
		}
	}
	if e = w.SavePoolRequest(r); e != nil {
		return e
	}
	cert, e := gateway.CollectPool(ctx, org, members, r)
	if e != nil {
		return e
	}
	b, e := cert.MarshalBinary()
	if e != nil {
		return e
	}
	f, e := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if *submit {
		if e = transport.NewCommitteeClient(n.CommitteeURLs[0], n.CommitteeURLs[1:]...).Submit(ctx, b); e != nil {
			return e
		}
	}
	fmt.Printf("certificate %s sequence %d saved to %s\n", cert.Action.ID(), cert.Action.Sequence, *output)
	return nil
}
func configuredPoolMembers(n cfg.Network, config protocol.Hash) (protocol.OrgConfig, [4]gateway.PoolApprover, error) {
	var members [4]gateway.PoolApprover
	for _, org := range n.Organizations {
		if org.Hash() != config {
			continue
		}
		urls, ok := n.Members[org.Org]
		if !ok {
			return org, members, protocol.ErrAuth
		}
		for i, u := range urls {
			members[i] = transport.NewMemberClient(u)
		}
		return org, members, nil
	}
	return protocol.OrgConfig{}, members, protocol.ErrAuth
}
func completePoolReceipt(ctx context.Context, w *wallet.Wallet, c protocol.PoolCertificate, members [4]gateway.PoolApprover) error {
	var installers [4]wallet.PoolInstaller
	for i := range installers {
		installers[i] = members[i]
	}
	return w.CompletePool(ctx, c, installers)
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
