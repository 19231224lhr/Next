package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"path/filepath"
	"time"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/blockfollow"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/transport"
	"utxo/internal/wallet"
	"utxo/protocol"
)

type liabilityReport struct {
	Mode                          string
	StartedUnixNS, FinishedUnixNS int64
	Hops                          [3]chainHop
	Repair                        repairTrial
	Obligations                   []rules.DirectObligation
	ParentPublished               bool
	Error                         string `json:",omitempty"`
}

func liabilityGenesis(n cfg.Network, desc [3]protocol.ReceiveDescriptor) cfg.Network {
	grants := n.Genesis.Grants[:0]
	for _, g := range n.Genesis.Grants {
		if g.Key.Kind == protocol.ResourceFUEL || g.Key.Kind == protocol.ResourcePolicy {
			continue
		}
		if g.Key.Kind == protocol.ResourceCAL {
			g.Amount = 60000
		}
		grants = append(grants, g)
	}
	n.Genesis.Grants = grants
	accounts := n.Accounts[:0]
	for _, a := range n.Accounts {
		if a.Asset == protocol.AssetFUEL {
			continue
		}
		if a.Asset == protocol.AssetCAL {
			a.Balance = 60000
		}
		accounts = append(accounts, a)
	}
	n.Accounts = accounts
	for i, d := range desc {
		id := protocol.OutputID(protocol.Digest("E3_SEQUENCE_FUEL", n.Genesis.Network[:], []byte{byte(i)}))
		n.Genesis.Outputs = append(n.Genesis.Outputs, state.OriginOutput{ID: id, Fact: protocol.Digest("E3_SEQUENCE_FINAL", id[:]), Output: protocol.Output{Asset: protocol.AssetFUEL, Amount: 10000, Recipient: d}})
	}
	return n
}

func liabilityDirect(args []string) (err error) {
	f := flag.NewFlagSet("liability-v4", flag.ContinueOnError)
	dir := f.String("dir", "", "fresh E3 laboratory")
	mode := f.String("case", "missing", "missing or cross")
	prepare := f.Bool("prepare", false, "prepare three self-funded wallets before node boot")
	audit := f.Bool("audit", false, "audit stopped sequence")
	if err = f.Parse(args); err != nil {
		return err
	}
	if *dir == "" || (*mode != "missing" && *mode != "cross") {
		return protocol.ErrRule
	}
	var lab cfg.Lab
	var n cfg.Network
	if err = cfg.Read(filepath.Join(*dir, "lab.json"), &lab); err != nil {
		return err
	}
	if err = cfg.Read(lab.Network, &n); err != nil {
		return err
	}
	if *audit {
		return auditLiability(*dir, lab, n)
	}
	if *prepare {
		if err = prepareChain(*dir, lab, n); err != nil {
			return err
		}
		if err = cfg.Read(lab.Network, &n); err != nil {
			return err
		}
	}
	var keys [3]ed25519.PrivateKey
	var desc [3]protocol.ReceiveDescriptor
	orgIndex := [3]int{0, 0, 0}
	if *mode == "cross" {
		orgIndex[1] = 1
	}
	for i, path := range []string{lab.Owners[0], lab.Owners[1], filepath.Join(*dir, "keys", "chain-c.key")} {
		keys[i], err = cfg.PrivateKey(path)
		if err != nil {
			return err
		}
		desc[i] = protocol.NewDescriptor(n.Genesis.Network, protocol.Route{Kind: protocol.OrgRoute, Org: n.Organizations[orgIndex[i]].Org}, keys[i])
	}
	if *prepare {
		return cfg.Write(lab.Network, liabilityGenesis(n, desc))
	}
	policy, err := n.Direct.Policy(n.Schedule, n.Organizations)
	if err != nil {
		return err
	}
	trust, err := n.Trust()
	if err != nil {
		return err
	}
	var wallets [3]*wallet.Wallet
	var dbs [3]*store.Group
	for i := range wallets {
		db, e := store.OpenNoSync(filepath.Join(*dir, fmt.Sprintf("liability-wallet-%d.db", i)), store.Identity{Network: n.ChainID, Role: "liability-wallet", Node: fmt.Sprint(i), Schema: 4})
		if e != nil {
			return e
		}
		g, e := store.NewGroup(db, 256, 64)
		if e != nil {
			db.Close()
			return e
		}
		dbs[i] = g
		defer g.Close()
		wallets[i], err = wallet.New(g, n.Genesis.Network, desc[i].Owner, n.Organizations)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := transport.NewHTTPClient(5 * time.Second)
	public := transport.NewCommitteeClient(n.CommitteeURLs[0], n.CommitteeURLs[1:]...)
	for i := range wallets {
		defer blockfollow.Start(ctx, cancel, dbs[i], transport.NewCommitteeClient(n.CommitteeURLs[0]), trust, wallets[i].PrepareBlock)()
	}
	var batches [2][4]*progressBatcher
	batchCtx, stopBatches := context.WithCancel(ctx)
	for org := 0; org < 2; org++ {
		for i, url := range n.Members[n.Organizations[org].Org] {
			batches[org][i] = newProgressBatcher(batchCtx, 4, func(ctx context.Context, facts []protocol.SpendFactID) ([]member.DirectStatus, error) {
				return loadProgressBatch(ctx, client, url, facts)
			})
		}
	}
	defer func() {
		stopBatches()
		for _, bs := range batches {
			for _, b := range bs {
				<-b.done
			}
		}
	}()
	report := liabilityReport{Mode: *mode, StartedUnixNS: time.Now().UnixNano()}
	start := time.Now()
	defer func() {
		report.FinishedUnixNS = time.Now().UnixNano()
		if err != nil {
			report.Error = err.Error()
		}
		if e := cfg.Write(filepath.Join(*dir, "reports", "liability-v4.json"), report); err == nil {
			err = e
		}
	}()
	var origin state.OriginOutput
	var fees [3]state.OriginOutput
	for _, o := range n.Genesis.Outputs {
		if o.Output.Asset == protocol.AssetCAL && o.Output.Recipient.Owner == desc[0].Owner {
			origin = o
		}
		for i := range fees {
			if o.Output.Asset == protocol.AssetFUEL && o.Output.Recipient.Owner == desc[i].Owner {
				fees[i] = o
			}
		}
	}
	var raw [3][]byte
	id := origin.ID
	for i := range report.Hops {
		sender, receiver := i, (i+1)%3
		coin := wallet.DirectCoin{Output: origin.Output, Final: origin.Fact}
		if i > 0 {
			err = dbs[sender].View(func(v state.ReadView) error {
				var found bool
				var e error
				coin, found, e = state.Load[wallet.DirectCoin](v, wallet.DirectCoinKey(id, 0))
				if e == nil && !found {
					return state.ErrNotFound
				}
				return e
			})
			if err != nil {
				return err
			}
		}
		in, proofs, e := chainInput(id, coin)
		if e != nil {
			return e
		}
		request, e := budgetRequest(n, policy, n.Organizations[orgIndex[sender]], keys[sender], in, coin.Output, desc[receiver], proofs, fees[sender])
		if e != nil {
			return e
		}
		h := &report.Hops[i]
		*h = chainHop{Hop: i + 1, Sender: sender, Receiver: receiver, Input: id, Tx: request.Tx.ID(), OutputData: request.Tx.Body.Outputs[0], CertificateInput: in.Kind == protocol.CertificateInput, SentUnixNS: time.Now().UnixNano()}
		if err = wallets[sender].SaveDirectRequest(request); err != nil {
			return err
		}
		encoded, e := request.MarshalBinary()
		if e != nil {
			return e
		}
		path := "/v3/transactions"
		if i == 0 || (*mode == "cross" && i == 1) {
			path = "/debug/budget/collect"
		}
		cert, e := budgetSend(ctx, client, lab.Gateways[orgIndex[sender]]+path, encoded)
		if e != nil {
			return e
		}
		if err = wallets[receiver].ReceiveDirect(h.OutputData, cert, 0); err != nil {
			return err
		}
		h.ReadyUnixNS = time.Now().UnixNano()
		h.FastMS = float64(h.ReadyUnixNS-h.SentUnixNS) / 1e6
		h.Fact, h.Output = cert.QC.Fact, cert.Summary.OutputID(0)
		raw[i], err = (protocol.DirectPayment{Tx: request.Tx, Certificate: cert, InputCertificates: proofs}).Submission().MarshalBinary()
		if err != nil {
			return err
		}
		id = h.Output
	}
	observe := func(i int, instance uint8) error {
		for {
			ok, e := wallets[(i+1)%3].DirectFinal(report.Hops[i].Output, instance)
			if e != nil {
				return e
			}
			if ok {
				now := time.Now()
				report.Hops[i].FinalUnixNS = now.UnixNano()
				report.Hops[i].FinalOffsetMS = float64(now.Sub(start)) / 1e6
				return nil
			}
			if e = budgetPause(ctx, 5*time.Millisecond); e != nil {
				return e
			}
		}
	}
	obligation := func(output protocol.OutputID, issuer protocol.Hash, status uint8) error {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, n.CommitteeURLs[0]+"/v3/obligations/"+protocol.Hash(output).String(), nil)
		if e != nil {
			return e
		}
		resp, e := client.Do(req)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("obligation HTTP %d", resp.StatusCode)
		}
		var ob rules.DirectObligation
		if e = json.NewDecoder(resp.Body).Decode(&ob); e != nil {
			return e
		}
		report.Obligations = append(report.Obligations, ob)
		if ob.Issuer != issuer || ob.Status != status || ob.Amount != 100 {
			return fmt.Errorf("incorrect direct obligation: %+v", ob)
		}
		return nil
	}
	// T2 becomes final while the older transactions remain unpublished.
	if err = observe(2, 0); err != nil {
		return err
	}
	publish := func(i int, instance uint8) error {
		if e := budgetRelease(ctx, time.Now(), func(c context.Context) error { return public.Submit(c, raw[i]) }, func() {}); e != nil {
			return e
		}
		retryCtx, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for budgetPause(retryCtx, 2*time.Second) == nil {
				_ = public.Submit(retryCtx, raw[i])
			}
		}()
		e := observe(i, instance)
		stop()
		<-done
		return e
	}
	if *mode == "cross" {
		if err = obligation(report.Hops[1].Output, n.Organizations[1].Org, rules.DirectOpen); err != nil {
			return err
		}
		if err = publish(1, 0); err != nil {
			return err
		}
		if err = obligation(report.Hops[1].Output, n.Organizations[1].Org, rules.DirectFulfilled); err != nil {
			return err
		}
	} else if err = observe(1, 0); err != nil {
		return err
	}
	if err = obligation(report.Hops[0].Output, n.Organizations[0].Org, rules.DirectOpen); err != nil {
		return err
	}
	if err = waitRepair(ctx, client, n.CommitteeURLs[:], report.Hops[0].Output, &report.Repair); err != nil {
		return err
	}
	if err = obligation(report.Hops[0].Output, n.Organizations[0].Org, rules.DirectRepaired); err != nil {
		return err
	}
	if *mode == "cross" {
		report.ParentPublished = true
		if err = publish(0, 1); err != nil {
			return err
		}
	} else if err = budgetPause(ctx, 3*time.Second); err != nil {
		return err
	}
	// Member closure is a separate endpoint; it must not delay the causal
	// parent publication sequence or overwrite the first finality observation.
	for i := range report.Hops {
		if i == 0 && *mode == "missing" {
			continue
		}
		var instance uint8
		if i == 0 {
			instance = 1
		}
		h := report.Hops[i]
		if err = observeChainHop(ctx, batches[orgIndex[i]], wallets[(i+1)%3], &h, start, make(chan struct{}), instance); err != nil {
			return err
		}
		report.Hops[i].MemberClosedUnixNS = h.MemberClosedUnixNS
		report.Hops[i].MemberOffsetMS = h.MemberOffsetMS
		report.Hops[i].ProgressErrors = h.ProgressErrors
	}
	final, e := wallets[0].DirectFinal(report.Hops[2].Output, 0)
	if e != nil {
		return e
	}
	if !final {
		return fmt.Errorf("grandchild no longer final")
	}
	return nil
}

func auditLiability(dir string, lab cfg.Lab, n cfg.Network) error {
	var r liabilityReport
	if err := cfg.Read(filepath.Join(dir, "reports", "liability-v4.json"), &r); err != nil {
		return err
	}
	if r.Error != "" || r.Hops[2].FinalUnixNS == 0 || len(r.Repair.Nodes) != 4 {
		return fmt.Errorf("incomplete sequence")
	}
	for _, node := range r.Repair.Nodes {
		if node.MaterializedUnixNS == 0 {
			return fmt.Errorf("physical repair incomplete")
		}
	}
	type result struct {
		Name   string
		Public int
		Fee    rules.Escrow
		CAL    [2]uint64
	}
	var results []result
	for _, node := range lab.Nodes {
		if node.Binary != "committee" {
			continue
		}
		out := result{Name: node.Name}
		err := store.Inspect(filepath.Join(dir, node.Name, "committee.db"), func(v state.ReadView) error {
			for i, org := range n.Organizations {
				balance, _, e := state.Load[uint64](v, rules.AccountKey(org.Org, protocol.AssetCAL))
				if e != nil {
					return e
				}
				out.CAL[i] = balance
				want := uint64(60000)
				if i == 0 {
					want -= 100
				}
				if balance != want {
					return fmt.Errorf("wrong issuer reserve debit")
				}
			}
			for i, h := range r.Hops {
				p, found, e := state.Load[rules.DirectPaymentState](v, state.Key(103, h.Fact[:]))
				if e != nil {
					return e
				}
				if i == 0 && r.Mode == "missing" {
					if found {
						return fmt.Errorf("withheld parent appeared")
					}
					continue
				}
				if !found || !p.Settled || !p.Fee.Closed || p.FeeSource != protocol.OwnerFinalUTXO {
					return fmt.Errorf("payment or self-paid fee incomplete")
				}
				out.Public++
				out.Fee.Maximum += p.Fee.Maximum
				out.Fee.Rewards += p.Fee.Rewards
				out.Fee.Burned += p.Fee.Burned
				out.Fee.Refunded += p.Fee.Refunded
			}
			ob, found, e := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(r.Hops[0].Output))
			if e != nil {
				return e
			}
			if !found || ob.Status != rules.DirectRepaired || ob.Issuer != n.Organizations[0].Org {
				return fmt.Errorf("lost paid liability")
			}
			if r.Mode == "cross" {
				ob, found, e = state.Load[rules.DirectObligation](v, rules.DirectObligationKey(r.Hops[1].Output))
				if e != nil {
					return e
				}
				if !found || ob.Status != rules.DirectFulfilled || ob.Issuer != n.Organizations[1].Org {
					return fmt.Errorf("responsibility transferred")
				}
			}
			coin, found, e := state.Load[state.Creation](v, rules.DirectCreationKey(r.Hops[2].Output, 0))
			if e != nil {
				return e
			}
			if !found || !coin.Final || coin.Output.Amount != 100 {
				return fmt.Errorf("grandchild changed")
			}
			if out.Fee.Rewards+out.Fee.Burned != uint64(out.Public)*94+5 || out.Fee.Maximum != out.Fee.Rewards+out.Fee.Burned+out.Fee.Refunded {
				return fmt.Errorf("wrong fees")
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("%s: %w", node.Name, err)
		}
		results = append(results, out)
	}
	return cfg.Write(filepath.Join(dir, "reports", "liability-audit.json"), results)
}
