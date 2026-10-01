package member

import (
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

// LocalProgress contains only this member's own signed obligations and fee plan.
type LocalProgress struct {
	Settled   bool
	Pending   uint32
	Paid      uint64
	Recovered uint64
	Fee       rules.Escrow
	Height    int64
	// Positional entries bind to the immutable Approval.Debits (resource,
	// grant version, worker and cap). Only this record changes during settlement.
	Applied []uint64 `json:",omitempty"`
}

// DirectStoreSchema prevents old member binaries from ignoring split credits.
const DirectStoreSchema uint64 = 6

func localApplied(a state.Approval, p LocalProgress) ([]uint64, error) {
	values := p.Applied
	if values == nil {
		values = make([]uint64, len(a.Debits))
		for i, d := range a.Debits {
			values[i] = d.Applied
		}
	}
	if len(values) != len(a.Debits) {
		return nil, rules.ErrAccounting
	}
	for i, d := range a.Debits {
		if values[i] < d.Applied || values[i] > d.Cap {
			return nil, rules.ErrAccounting
		}
	}
	return values, nil
}

// AppliedDebits is the shared read path for status and offline accounting.
// Old approvals retain their recorded cumulative amount; it is never reset.
func AppliedDebits(v state.ReadView, a state.Approval) ([]uint64, error) {
	var p LocalProgress
	if a.Direct != nil {
		var err error
		p, _, err = state.Load[LocalProgress](v, ProgressKey(a.Fact))
		if err != nil {
			return nil, err
		}
	}
	return localApplied(a, p)
}

func ProgressKey(f protocol.SpendFactID) []byte { return state.Key(122, f[:]) }
func waitingKey(id protocol.OutputID) []byte    { return state.Key(123, id[:]) }

func (m *Member) finishLocal(o *state.Overlay, a *state.Approval, p *LocalProgress) error {
	var err error
	p.Applied, err = localApplied(*a, *p)
	if err != nil {
		return err
	}
	if p.Settled && p.Pending == 0 && !p.Fee.Closed {
		next, err := p.Fee.Complete(rules.FeeClose)
		if err != nil {
			return err
		}
		p.Fee = next
	}
	for i := range a.Debits {
		d := &a.Debits[i]
		target := uint64(0)
		if d.Key.Kind == protocol.ResourceCAL && p.Settled {
			net, err := protocol.Sub(p.Paid, p.Recovered)
			if err != nil {
				return err
			}
			target, err = protocol.Sub(d.Cap, net)
			if err != nil {
				return err
			}
		}
		if d.Key.Kind != protocol.ResourceCAL && p.Fee.Closed {
			target = d.Cap
			if d.Key.Kind == protocol.ResourceFUEL || d.Key.Kind == protocol.ResourcePolicy {
				paid, err := protocol.Add(p.Fee.Rewards, p.Fee.Burned)
				if err != nil {
					return err
				}
				target, err = protocol.Sub(target, paid)
				if err != nil {
					return err
				}
			}
		}
		if target < p.Applied[i] || target > d.Cap {
			return rules.ErrAccounting
		}
		delta := target - p.Applied[i]
		if delta == 0 {
			continue
		}
		key := state.SliceKey(d.Key, d.Worker)
		slice, found, err := state.Load[state.Slice](o, key)
		if err != nil {
			return err
		}
		if !found {
			return rules.ErrMissing
		}
		slice.Reserved, err = protocol.Sub(slice.Reserved, delta)
		if err != nil {
			return err
		}
		slice.Available, err = protocol.Add(slice.Available, delta)
		if err != nil {
			return err
		}
		p.Applied[i] = target
		if err = state.Put(o, key, slice); err != nil {
			return err
		}
	}
	return state.Put(o, ProgressKey(a.Fact), *p)
}
func (m *Member) resolveLocal(o *state.Overlay, id protocol.OutputID, paid bool) error {
	fact, found, err := state.Load[protocol.SpendFactID](o, waitingKey(id))
	if err != nil || !found {
		return err
	}
	a, found, err := state.Load[state.Approval](o, state.Key(state.KeyApproval, fact[:]))
	if err != nil {
		return err
	}
	if !found {
		return rules.ErrMissing
	}
	p, found, err := state.Load[LocalProgress](o, ProgressKey(fact))
	if err != nil {
		return err
	}
	if !found || p.Pending == 0 {
		return rules.ErrAccounting
	}
	p.Pending--
	if paid {
		p.Fee.Held, err = protocol.Sub(p.Fee.Held, m.direct.RepairCost)
		if err != nil {
			return err
		}
		p.Fee.Rewards, err = protocol.Add(p.Fee.Rewards, m.direct.RepairCost)
		if err != nil {
			return err
		}
	}
	if err = m.finishLocal(o, &a, &p); err != nil {
		return err
	}
	o.Apply([]state.Change{{Key: waitingKey(id), Delete: true}})
	return nil
}

// PrepareBlock decodes and authenticates owned block bytes before entering the writer.
func (m *Member) PrepareBlock(b finality.VerifiedBlock) (blockfollow.Apply, error) {
	if m.direct == nil {
		return nil, protocol.ErrUnsupported
	}
	var updates []blockfollow.Apply
	for _, entry := range b.Transactions() {
		if entry.Code == 0 && protocol.IsReserveIncrease(entry.Bytes) {
			c, err := protocol.DecodeReserveIncrease(entry.Bytes)
			if err != nil {
				return nil, err
			}
			if c.Network != m.cfg.Organization.Network {
				return nil, protocol.ErrAuth
			}
			updates = append(updates, func(o *state.Overlay) error {
				return rules.ApplyReserveIncrease(o, c, m.cfg.Organization.Hash(), m.cfg.Workers)
			})
			continue
		}
		if entry.Code != 0 || len(entry.Data) == 0 {
			continue
		}
		result, effects, err := protocol.DecodePublicExecution(m.cfg.Organization.Network, entry.Bytes, entry.Data)
		if err != nil {
			return nil, err
		}
		if !result.Applied {
			continue
		}
		for _, fee := range result.FeeOutputs {
			if fee.Output.Recipient.Verify(m.cfg.Organization.Network) != nil {
				return nil, protocol.ErrAuth
			}
			if fee.Output.Recipient.Route.Org != m.cfg.Organization.Org {
				continue
			}
			updates = append(updates, func(o *state.Overlay) error {
				id := protocol.OutputIdentity(m.cfg.Organization.Network, fee.Transaction, fee.Index)
				c := state.Creation{Output: fee.Output, Final: true, Fact: protocol.CreationIdentity(m.cfg.Organization.Network, fee.Transaction, fee.Index, 0)}
				return state.Put(o, rules.DirectCreationKey(id, 0), c)
			})
		}
		if protocol.IsRepairBatch(entry.Bytes) {
			for _, effect := range effects {
				updates = append(updates, func(o *state.Overlay) error { return m.applyRepairEffect(o, effect) })
			}
		} else if protocol.IsRepairInput(entry.Bytes) {
			repair, err := protocol.DecodeRepairInput(entry.Bytes)
			if err != nil {
				return nil, err
			}
			pay, err := protocol.DecodeDirectSubmission(repair.TransactionBytes)
			if err != nil {
				return nil, err
			}
			if int(repair.Input) >= len(pay.Tx.Body.Inputs) {
				return nil, protocol.ErrRule
			}
			updates = append(updates, func(o *state.Overlay) error { return m.applyRepair(o, repair, pay) })
		} else {
			pay, err := protocol.DecodeDirectSubmission(entry.Bytes)
			if err != nil {
				return nil, err
			}
			updates = append(updates, func(o *state.Overlay) error { return m.applyPayment(o, pay, result, b.Height()) })
		}
	}
	return func(o *state.Overlay) error {
		for _, apply := range updates {
			if err := apply(o); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

func (m *Member) applyRepair(o *state.Overlay, repair protocol.RepairInput, pay protocol.DirectSubmission) error {
	return m.applyRepairEffect(o, protocol.RepairEffect{Output: repair.Output, ParentFact: protocol.SpendFactID(pay.Tx.Body.Inputs[repair.Input].Evidence), ConsumerFact: pay.Summary().Fact(), Amount: pay.Tx.Claims[repair.Input].Output.Amount})
}

func (m *Member) applyRepairEffect(o *state.Overlay, effect protocol.RepairEffect) error {
	if waiting, found, err := state.Load[protocol.SpendFactID](o, waitingKey(effect.Output)); err != nil {
		return err
	} else if found && waiting != effect.ConsumerFact {
		return rules.ErrAccounting
	}
	fact := effect.ParentFact
	a, found, err := state.Load[state.Approval](o, state.Key(state.KeyApproval, fact[:]))
	if err != nil {
		return err
	}
	if found {
		p, _, err := state.Load[LocalProgress](o, ProgressKey(fact))
		if err != nil {
			return err
		}
		p.Paid, err = protocol.Add(p.Paid, effect.Amount)
		if err != nil {
			return err
		}
		if err = m.finishLocal(o, &a, &p); err != nil {
			return err
		}
	}
	if err = m.resolveLocal(o, effect.Output, true); err != nil {
		return err
	}
	return nil
}

func (m *Member) applyPayment(o *state.Overlay, pay protocol.DirectSubmission, result protocol.ExecutionResult, height int64) error {
	tx := pay.Tx
	fact := pay.Authorization.Fact
	if tx.Body.Network != m.cfg.Organization.Network {
		return protocol.ErrAuth
	}
	recovered := make(map[uint32]bool, len(result.RecoveredOutputs))
	var returned uint64
	for _, i := range result.RecoveredOutputs {
		if int(i) >= len(tx.Body.Outputs) || recovered[i] {
			return protocol.ErrRule
		}
		recovered[i] = true
		var err error
		returned, err = protocol.Add(returned, tx.Body.Outputs[i].Amount)
		if err != nil {
			return err
		}
	}
	a, found, err := state.Load[state.Approval](o, state.Key(state.KeyApproval, fact[:]))
	if err != nil {
		return err
	}
	if found {
		p, _, err := state.Load[LocalProgress](o, ProgressKey(fact))
		if err != nil {
			return err
		}
		if p.Settled {
			return rules.ErrConflict
		}
		if p.Paid > returned {
			return rules.ErrAccounting
		}
		// A signer joining after compensation never occupied the earlier paid
		// amount. Restore only its own recorded loss, not another member's.
		p.Recovered = p.Paid
		p.Settled = true
		p.Height = height
		p.Pending = uint32(len(result.MissingInputs))
		p.Fee, err = rules.NewEscrow(tx.Body.Fee.Maximum, m.direct.Base.Fee)
		if err != nil {
			return err
		}
		for _, stage := range []rules.FeeAction{rules.FeeRegister, rules.FeeSettle} {
			p.Fee, err = p.Fee.Complete(stage)
			if err != nil {
				return err
			}
		}
		for _, i := range result.MissingInputs {
			if int(i) >= len(tx.Body.Inputs) {
				return protocol.ErrRule
			}
			if err = state.Put(o, waitingKey(tx.Body.Inputs[i].Output), fact); err != nil {
				return err
			}
		}
		if err = m.finishLocal(o, &a, &p); err != nil {
			return err
		}
	}
	if tx.Body.Config == m.cfg.Organization.Hash() {
		if err = state.Put(o, state.Key(state.KeyObserved, fact[:]), true); err != nil {
			return err
		}
		o.Apply([]state.Change{{Key: state.Key(state.KeyOutbox, fact[:]), Delete: true}})
		for i, in := range tx.Body.Inputs {
			if err = state.Put(o, rules.DirectSpendKey(in.Output, tx.Claims[i].Instance), state.Spend{Consumed: fact}); err != nil {
				return err
			}
		}
	}
	if tx.Body.Config == m.cfg.Organization.Hash() {
		for _, in := range tx.Body.Fee.Inputs {
			if err = state.Put(o, rules.DirectSpendKey(in.Output, 0), state.Spend{Consumed: fact}); err != nil {
				return err
			}
		}
	}
	for i, out := range tx.Body.Outputs {
		id := pay.Summary().OutputID(uint32(i))
		if err = m.resolveLocal(o, id, false); err != nil {
			return err
		}
		if recovered[uint32(i)] || out.Recipient.Route.Org != m.cfg.Organization.Org {
			continue
		}
		creation := state.Creation{Output: out, Fact: protocol.CreationIdentity(tx.Body.Network, tx.ID(), uint32(i), 0), Source: protocol.Hash(fact), Final: true}
		if err = state.Put(o, rules.DirectCreationKey(id, 0), creation); err != nil {
			return err
		}
	}
	return nil
}
