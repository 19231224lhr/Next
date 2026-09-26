package member_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"utxo/crypto/chameleon"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/committee"
	"utxo/internal/member"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

// This is a cross-layer trace, not a BFT/cryptographic proof. Three honest
// member implementations follow authenticated test blocks at different speeds;
// member 3 is a Byzantine signer and intentionally has no local budget state.
func TestSecurityComposition(t *testing.T) {
	for _, workers := range []uint32{1, 3} {
		for _, extraApproval := range []bool{false, true} {
			for _, repair := range []bool{false, true} {
				t.Run(fmt.Sprintf("workers%d/extra%v/repair%v", workers, extraApproval, repair), func(t *testing.T) {
					securityComposition(t, workers, extraApproval, repair)
				})
			}
		}
	}
}

func securityComposition(t *testing.T, workers uint32, extraApproval, repair bool) {
	f := testkit.NewFixture("security-composition", "org", 2)
	f.EnableDirect()
	f.Genesis.Grants[0].Amount = 901
	g := f.Genesis.Grants[0]
	rawKey, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	pemBlock, _ := pem.Decode(rawKey)
	key, err := x509.ParsePKCS1PrivateKey(pemBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	settings := rules.DirectSettings{Modulus: key.N.Bytes(), TimeoutSeconds: 30, RepairCost: 5}
	policy, err := settings.Policy(f.Schedule, []protocol.OrgConfig{f.Org})
	if err != nil {
		t.Fatal(err)
	}
	var members [3]*member.Member
	var localDB [3]store.Store
	for i := range members {
		localDB[i] = store.NewMemory()
		defer localDB[i].Close()
		members[i], err = member.New(member.Config{Organization: f.Org, Index: uint16(i), Key: f.Keys[i], Peers: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Workers: workers, Direct: &settings}, localDB[i], f.Genesis)
		if err != nil {
			t.Fatal(err)
		}
	}
	ledger := store.NewMemory()
	defer ledger.Close()
	funding := protocol.ReserveFundingAccount(f.Org.Network, g.Subject)
	_, err = committee.NewEngine(committee.EngineConfig{Network: f.Org.Network, Organizations: []protocol.OrgConfig{f.Org}, Schedule: f.Schedule, Genesis: f.Genesis, Direct: &settings, Accounts: []committee.GenesisAccount{
		{Owner: f.Org.Org, Asset: protocol.AssetCAL, Balance: 901},
		{Owner: funding, Asset: protocol.AssetCAL, Balance: 3},
		{Owner: f.Org.Org, Asset: protocol.AssetFUEL, Balance: 1000000000},
	}}, ledger)
	if err != nil {
		t.Fatal(err)
	}
	var payments []protocol.DirectPayment
	certify := func(tx protocol.FastTx, parents []protocol.InputCertificate) protocol.DirectPayment {
		t.Helper()
		var c protocol.OutputCertificate
		for i, m := range members {
			if i == 2 && !extraApproval {
				continue
			}
			a, err := m.ApproveDirect(context.Background(), protocol.DirectRequest{Tx: tx, InputCertificates: parents})
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				c.Summary = a.Summary
				c.QC.Fact = a.Summary.Fact()
			}
			if a.Summary.Fact() != c.QC.Fact {
				t.Fatal("approval identity mismatch")
			}
			if i < 2 {
				c.QC.Votes = append(c.QC.Votes, a.Vote)
			}
		}
		c.QC.Votes = append(c.QC.Votes, protocol.SignSpend(c.QC.Fact, 3, f.Keys[3]))
		if err := c.Verify(f.Org); err != nil {
			t.Fatal(err)
		}
		p := protocol.DirectPayment{Tx: tx, Certificate: c, InputCertificates: parents}
		payments = append(payments, p)
		return p
	}
	parent, err := f.FastTransaction(0, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	body := parent.Body
	body.Outputs = []protocol.Output{body.Outputs[0], body.Outputs[0]}
	body.Outputs[0].Amount, body.Outputs[1].Amount = 40, 60
	body.Intent = body.IntentID()
	parent, err = protocol.NewFastTx(body, parent.Claims, policy.Key)
	if err != nil {
		t.Fatal(err)
	}
	parent.Auth = []protocol.OwnerAuth{protocol.SignOwner(parent.ID(), f.Owner)}
	pp := certify(parent, nil)
	body = parent.Body
	body.Inputs = []protocol.Input{{Kind: protocol.CertificateInput, Output: pp.Certificate.Summary.OutputID(0), Evidence: protocol.Hash(pp.Certificate.QC.Fact)}}
	body.Outputs = []protocol.Output{body.Outputs[0]}
	body.Nonce[0] = 42
	body.Intent = body.IntentID()
	child, err := protocol.NewFastTx(body, []protocol.InputClaim{{Output: body.Outputs[0]}}, policy.Key)
	if err != nil {
		t.Fatal(err)
	}
	child.Auth = []protocol.OwnerAuth{protocol.SignOwner(child.ID(), f.Owner)}
	cp := certify(child, []protocol.InputCertificate{{Certificate: pp.Certificate}})
	settled := make(map[protocol.SpendFactID]bool)
	paid := make(map[protocol.SpendFactID]uint64)
	budget, spent, reserved := uint64(901), uint64(0), uint64(0)
	check := func() {
		t.Helper()
		var risk, residualSum uint64
		var localDebits [3]uint64
		for _, p := range payments {
			w := paid[p.Certificate.QC.Fact]
			if !settled[p.Certificate.QC.Fact] {
				w = 0
				for _, out := range p.Tx.Body.Outputs {
					w += out.Amount
				}
			}
			risk += w
			for i := range members {
				if err := localDB[i].View(func(v state.ReadView) error {
					a, found, err := state.Load[state.Approval](v, state.Key(state.KeyApproval, p.Certificate.QC.Fact[:]))
					if err != nil {
						return err
					}
					if !found {
						if i != 2 || extraApproval {
							t.Fatal("missing approval")
						}
						return nil
					}
					applied, err := member.AppliedDebits(v, a)
					if err != nil {
						return err
					}
					for j, d := range a.Debits {
						if d.Key.Kind == protocol.ResourceCAL {
							if applied[j] > d.Cap {
								t.Fatal("applied exceeds original cap")
							}
							r := d.Cap - applied[j]
							if r < w {
								t.Fatalf("member%d residual%d < risk%d", i, r, w)
							}
							residualSum += r
							localDebits[i] += r
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if spent > risk || 2*risk > residualSum || risk > budget || reserved > risk-spent {
			t.Fatal("risk composition failed")
		}
		for i, m := range members {
			var sum, localResidual uint64
			for w := uint32(0); w < workers; w++ {
				s, err := m.Quota(g.Key, w)
				if err != nil {
					t.Fatal(err)
				}
				sum += s.Available + s.Reserved
				localResidual += s.Reserved
			}
			if i == 2 && !extraApproval && localResidual != 0 {
				t.Fatal("unapproved member reserved")
			}
			if localResidual != localDebits[i] {
				t.Fatal("Worker reserved differs from original approval residuals")
			}
			if err := localDB[i].View(func(v state.ReadView) error {
				grant, _, err := state.Load[state.Grant](v, state.Key(state.KeyGrant, g.Key.Encode()))
				if err != nil {
					return err
				}
				share, err := protocol.GrantShare(grant.Amount)
				if err != nil {
					return err
				}
				if sum != share || grant.Amount > budget {
					t.Fatal("local budget mismatch")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		if err := ledger.View(func(v state.ReadView) error {
			u, _, err := state.Load[rules.PublicUsage](v, state.Key(state.KeyUsage, g.Key.Encode()))
			if err != nil {
				return err
			}
			balance, _, err := state.Load[uint64](v, rules.AccountKey(f.Org.Org, protocol.AssetCAL))
			if u.Spent != spent || u.Reserved != reserved || balance != budget-spent {
				t.Fatalf("public bridge: %+v balance=%d", u, balance)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	check() // Both complete QCs exist, but neither is publicly registered.
	var blocks []finality.VerifiedBlock
	var previous []byte
	appendBlock := func(raw []byte, tr state.Transition) {
		trust, data := testkit.Block("security-composition", int64(len(blocks)+1), previous, [][]byte{raw}, []*abci.ExecTxResult{{Data: tr.Data}})
		b, err := finality.VerifyBlock(trust, data)
		if err != nil {
			t.Fatal(err)
		}
		blocks = append(blocks, b)
		previous = b.Hash()
	}
	follow := func(i int, b finality.VerifiedBlock) {
		if err := blockfollow.Commit(localDB[i], b, members[i].PrepareBlock); err != nil {
			t.Fatal(err)
		}
		check()
	}
	execute := func(p protocol.DirectPayment, now int64) {
		v, err := rules.VerifyDirectPayment(p, policy)
		if err != nil {
			t.Fatal(err)
		}
		var tr state.Transition
		err = ledger.Update(func(view state.ReadView) ([]state.Change, error) {
			var e error
			tr, e = rules.EvaluateDirectPayment(view, v, policy, now)
			return tr.Changes, e
		})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := p.Submission().MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		settled[p.Certificate.QC.Fact] = true
		appendBlock(raw, tr)
	}
	execute(cp, 100)
	reserved = 100
	check()
	follow(0, blocks[0])
	if repair {
		var tr state.Transition
		err = ledger.Update(func(v state.ReadView) ([]state.Change, error) {
			var e error
			tr, e = rules.EvaluateDirectCompensation(v, pp.Certificate.Summary.OutputID(0), policy, 131)
			return tr.Changes, e
		})
		if err != nil {
			t.Fatal(err)
		}
		paid[pp.Certificate.QC.Fact], spent, reserved = 40, 40, 60
		raw, err := cp.Submission().MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		command := protocol.RepairInput{Network: f.Org.Network, Output: pp.Certificate.Summary.OutputID(0), Height: 1, TransactionBytes: raw, Parts: []chameleon.Opening{{}}}
		raw, err = command.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		// Public repair economic result is the boundary here. The synthetic
		// repair body is for follower decoding; redaction.Execute has separate tests.
		appendBlock(raw, tr)
		check()
		follow(0, blocks[len(blocks)-1])
	}
	for _, amount := range []uint64{1, 2} {
		c := protocol.ReserveIncrease{Network: f.Org.Network, Organization: f.Org.Hash(), Key: g.Key, Grant: g.ID, Previous: budget, Amount: amount}
		c.Sign(f.Owner)
		var tr state.Transition
		err = ledger.Update(func(v state.ReadView) ([]state.Change, error) {
			var e error
			tr, e = rules.EvaluateReserveIncrease(v, c, map[string]struct{}{string(rules.AccountKey(f.Org.Org, protocol.AssetCAL)): {}})
			return tr.Changes, e
		})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := c.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		budget += amount
		appendBlock(raw, tr)
		check()
		follow(0, blocks[len(blocks)-1])
	}
	execute(pp, 132)
	reserved = 0
	check()
	// Skipping prior public events must fail without releasing any budget.
	if err := blockfollow.Commit(localDB[1], blocks[len(blocks)-1], members[1].PrepareBlock); err == nil {
		t.Fatal("skipped prefix accepted")
	}
	check()
	follow(0, blocks[len(blocks)-1])
	for _, i := range []int{2, 1} {
		for _, b := range blocks {
			follow(i, b)
			follow(i, b)
		}
	}
	// Reuse released quota for a new independently funded payment.
	tx, err := f.FastTransaction(1, 99, policy)
	if err != nil {
		t.Fatal(err)
	}
	last := certify(tx, nil)
	check()
	execute(last, 140)
	check()
	for i := range members {
		follow(i, blocks[len(blocks)-1])
	}
}
