package rules

import (
	"testing"
	"utxo/internal/state"
	"utxo/protocol"
)

func poolDeposit(t *testing.T, f directFixture) protocol.PoolCertificate {
	t.Helper()
	a := protocol.PoolAction{Kind: protocol.PoolDeposit, Network: f.org.Network, Config: f.org.Hash(), Input: f.genesis, Evidence: protocol.Digest("genesis-fact"), Amount: 100, To: f.output.Recipient}
	a.Pool = protocol.PoolIdentity(a.Network, a.Config, a.Input)
	a.Sign(f.owner)
	return poolCert(f, a)
}
func poolCert(f directFixture, a protocol.PoolAction) protocol.PoolCertificate {
	c := protocol.PoolCertificate{Action: a}
	for i := 0; i < 3; i++ {
		c.Votes = append(c.Votes, protocol.SignPool(a, uint16(i), f.members[i]))
	}
	return c
}
func applyPool(t *testing.T, f directFixture, c protocol.PoolCertificate) error {
	t.Helper()
	return f.db.Update(func(v state.ReadView) ([]state.Change, error) {
		tr, e := EvaluatePool(v, c, f.org.Network, f.policy.Organizations)
		return tr.Changes, e
	})
}
func TestPoolDepositExitConservesAndRejectsInternalReceipt(t *testing.T) {
	f := newDirectFixture(t)
	d := poolDeposit(t, f)
	if err := applyPool(t, f, d); err != nil {
		t.Fatal(err)
	}
	// The original UTXO can no longer fund ordinary payments.
	pay := f.payment(t, f.genesis, nil, 1)
	if err := f.db.View(func(v state.ReadView) error {
		verified, e := VerifyDirectPayment(pay, f.policy)
		if e != nil {
			return e
		}
		_, e = EvaluateDirectPayment(v, verified, f.policy, 1)
		return e
	}); err == nil {
		t.Fatal("locked UTXO spent")
	}
	move := protocol.PoolAction{Kind: protocol.PoolTransfer, Network: d.Action.Network, Config: d.Action.Config, Pool: d.Action.Pool, Sequence: 1, Previous: d.Action.ID(), Amount: 100, To: f.output.Recipient}
	move.Sign(f.owner)
	if err := applyPool(t, f, poolCert(f, move)); err == nil {
		t.Fatal("internal receipt publicly redeemable")
	}
	exit := move
	exit.Kind = protocol.PoolExit
	exit.Sign(f.owner)
	qc := poolCert(f, exit)
	if err := applyPool(t, f, qc); err != nil {
		t.Fatal(err)
	}
	if err := applyPool(t, f, qc); err != nil {
		t.Fatal("exact replay", err)
	}
	exit.Sequence++
	exit.Sign(f.owner)
	if err := applyPool(t, f, poolCert(f, exit)); err == nil {
		t.Fatal("double exit")
	}
	if err := f.db.View(func(v state.ReadView) error {
		c, found, e := state.Load[state.Creation](v, DirectCreationKey(qc.Action.OutputID(), 0))
		if e != nil {
			return e
		}
		if !found || !c.Final || c.Output.Amount != 100 {
			t.Fatal("missing final exit UTXO")
		}
		balance, _, e := state.Load[uint64](v, PoolEscrowKey(d.Action.Pool))
		if balance != 0 {
			t.Fatal("escrow not consumed")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}
func TestPoolRejectsUnbackedOrUnauthenticatedEntry(t *testing.T) {
	f := newDirectFixture(t)
	d := poolDeposit(t, f)
	bad := d
	bad.Votes = bad.Votes[:2]
	if applyPool(t, f, bad) == nil {
		t.Fatal("two votes")
	}
	bad = d
	bad.Action.Amount++
	bad.Action.Sign(f.owner)
	bad = poolCert(f, bad.Action)
	if applyPool(t, f, bad) == nil {
		t.Fatal("inflated deposit")
	}
	bad = d
	bad.Action.Input = protocol.OutputID(protocol.Digest("unknown"))
	bad.Action.Pool = protocol.PoolIdentity(bad.Action.Network, bad.Action.Config, bad.Action.Input)
	bad.Action.Sign(f.owner)
	bad = poolCert(f, bad.Action)
	if applyPool(t, f, bad) == nil {
		t.Fatal("unbacked deposit")
	}
}
