package member

import (
	"encoding/binary"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

const approvalExpiryPrefix byte = 236

var approvalClockKey = state.Key(237)

func registeredApprovalKey(f protocol.SpendFactID) []byte { return state.Key(238, f[:]) }
func approvalExpiryKey(deadline int64, fact protocol.SpendFactID) []byte {
	var payload [40]byte
	binary.BigEndian.PutUint64(payload[:8], uint64(deadline))
	copy(payload[8:], fact[:])
	return state.Key(approvalExpiryPrefix, payload[:])
}

// Called inside the existing authenticated block-follow transaction, after all
// successful executions have marked their whole-certificate registrations.
// The ordered queue bounds maintenance to 128 due approvals per block.
func (m *Member) expireApprovals(o *state.Overlay, now, height int64) error {
	if now <= 0 {
		return protocol.ErrRule
	}
	previous, _, err := state.Load[int64](o, approvalClockKey)
	if err != nil {
		return err
	}
	if now < previous {
		return protocol.ErrRule
	}
	if err := state.Put(o, approvalClockKey, now); err != nil {
		return err
	}
	prefix := state.Key(approvalExpiryPrefix)
	rows, err := o.Scan(prefix, nil, 1)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	first := protocol.NewDecoder(rows[0].Key[len(prefix):])
	payload := first.Bytes(40)
	if first.Done() != nil || len(payload) != 40 {
		return protocol.ErrEncoding
	}
	if int64(binary.BigEndian.Uint64(payload[:8])) > now {
		return nil
	}
	rows, err = o.Scan(prefix, nil, 128)
	if err != nil {
		return err
	}
	for _, row := range rows {
		d := protocol.NewDecoder(row.Key[len(prefix):])
		payload := d.Bytes(40)
		if d.Done() != nil || len(payload) != 40 {
			return protocol.ErrEncoding
		}
		if int64(binary.BigEndian.Uint64(payload[:8])) > now {
			break
		}
		var fact protocol.SpendFactID
		copy(fact[:], payload[8:])
		if err := m.expireApproval(o, fact, now, height); err != nil {
			return err
		}
		o.Delete(row.Key)
	}
	return nil
}

func (m *Member) expireApproval(o *state.Overlay, fact protocol.SpendFactID, now, height int64) error {
	a, found, err := state.Load[state.Approval](o, state.Key(state.KeyApproval, fact[:]))
	if err != nil {
		return err
	}
	if !found || a.Direct == nil || a.Direct.Body.ApprovalDeadline <= 0 || a.Direct.Body.ApprovalDeadline > now {
		return protocol.ErrRule
	}
	// INSTALL/outbox are relay state, not public registration. Public execution
	// and registration are permanent exclusions even if every output is resolved.
	for _, key := range [][]byte{registeredApprovalKey(fact), state.Key(state.KeyObserved, fact[:])} {
		if _, err := o.Get(key); err == nil {
			return nil
		} else if err != state.ErrNotFound {
			return err
		}
	}
	p, _, err := state.Load[LocalProgress](o, ProgressKey(fact))
	if err != nil {
		return err
	}
	if p.Invalidated {
		return nil
	}
	if p.Settled || p.Pending != 0 || p.Paid != 0 || p.Recovered != 0 || p.C3 != nil || p.Fee != (rules.Escrow{}) {
		return rules.ErrAccounting
	}
	p.Applied, err = localApplied(a, p)
	if err != nil {
		return err
	}
	for i, d := range a.Debits {
		delta := d.Cap - p.Applied[i]
		if delta == 0 {
			continue
		}
		key := state.SliceKey(d.Key, d.Worker)
		s, found, err := state.Load[state.Slice](o, key)
		if err != nil {
			return err
		}
		if !found {
			return rules.ErrMissing
		}
		s.Reserved, err = protocol.Sub(s.Reserved, delta)
		if err != nil {
			return err
		}
		s.Available, err = protocol.Add(s.Available, delta)
		if err != nil {
			return err
		}
		if err = state.Put(o, key, s); err != nil {
			return err
		}
		p.Applied[i] = d.Cap
	}
	unlock := func(id protocol.OutputID, instance uint8) error {
		key := rules.DirectSpendKey(id, instance)
		s, _, err := state.Load[state.Spend](o, key)
		if err != nil {
			return err
		}
		if s.Candidate == fact {
			s.Candidate = protocol.SpendFactID{}
		}
		if s.Consumed == fact {
			s.Consumed = protocol.SpendFactID{}
		} // unpublished INSTALL
		return state.Put(o, key, s)
	}
	for i, in := range a.Direct.Body.Inputs {
		if err := unlock(in.Output, a.Direct.Claims[i].Instance); err != nil {
			return err
		}
	}
	for _, in := range a.Direct.Body.Fee.Inputs {
		if err := unlock(in.Output, 0); err != nil {
			return err
		}
	}
	o.Delete(state.Key(state.KeyOutbox, fact[:]))
	o.Delete(approvalCollectionKey(fact))
	o.Delete(state.Key(state.KeyInstall, fact[:]))
	p.Invalidated, p.InvalidatedHeight = true, height
	return state.Put(o, ProgressKey(fact), p)
}

func (m *Member) checkApprovalTime(v state.ReadView, deadline int64) error {
	if deadline == 0 {
		if m.direct.RequireApprovalDeadline {
			return protocol.ErrRule
		}
		return nil
	}
	now, _, err := state.Load[int64](v, approvalClockKey)
	if err != nil {
		return err
	}
	if now >= deadline {
		return protocol.ErrExpired
	}
	return nil
}
