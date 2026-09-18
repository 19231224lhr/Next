package committee

import (
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

const MaxDependencyActions = 32

type dependencyQueue struct{ Head, Tail uint64 }

func queueItem(index uint64) []byte {
	e := new(protocol.Encoder)
	e.U64(index)
	return state.Key(state.KeyDependencyItem, e.Data())
}
func (e *Engine) enqueue(v state.ReadView, tr state.Transition) (state.Transition, error) {
	o := state.NewOverlay(v)
	o.Apply(tr.Changes)
	queue, _, err := state.Load[dependencyQueue](o, state.Key(state.KeyDependencyQueue))
	if err != nil {
		return state.Transition{}, err
	}
	changed := false
	for _, fact := range tr.Facts {
		if fact.Kind == protocol.FactOutputCreated {
			waitingKey := state.Key(state.KeyWaiting, fact.Key[:])
			child, found, err := state.Load[protocol.SpendFactID](o, waitingKey)
			if err != nil {
				return state.Transition{}, err
			}
			if !found {
				continue
			}
			o.Delete(waitingKey)
			queued, _, err := state.Load[bool](o, state.Key(state.KeyDependencyQueued, child[:]))
			if err != nil {
				return state.Transition{}, err
			}
			if queued {
				continue
			}
			if err = state.Put(o, queueItem(queue.Tail), child); err != nil {
				return state.Transition{}, err
			}
			queue.Tail, err = protocol.Add(queue.Tail, 1)
			if err != nil {
				return state.Transition{}, err
			}
			changed = true
			if err = state.Put(o, state.Key(state.KeyDependencyQueued, child[:]), true); err != nil {
				return state.Transition{}, err
			}
		}
	}
	if changed {
		if err = state.Put(o, state.Key(state.KeyDependencyQueue), queue); err != nil {
			return state.Transition{}, err
		}
	}
	return state.Transition{Changes: o.Changes(), Facts: tr.Facts}, nil
}

// Drain runs at block end under the same deterministic overlay. A bounded number
// of already-funded dependent actions are woken; remaining cursor state survives
// across blocks, including empty blocks. No new transport retry is necessary.
func (e *Engine) Drain(v state.ReadView) (state.Transition, error) {
	o := state.NewOverlay(v)
	var facts []protocol.FinalFact
	for attempts := 0; attempts < MaxDependencyActions; attempts++ {
		queue, _, err := state.Load[dependencyQueue](o, state.Key(state.KeyDependencyQueue))
		if err != nil {
			return state.Transition{}, err
		}
		if queue.Head == queue.Tail {
			break
		}
		child, found, err := state.Load[protocol.SpendFactID](o, queueItem(queue.Head))
		if err != nil {
			return state.Transition{}, err
		}
		if !found {
			return state.Transition{}, rules.ErrMissing
		}
		o.Delete(queueItem(queue.Head))
		queue.Head, err = protocol.Add(queue.Head, 1)
		if err != nil {
			return state.Transition{}, err
		}
		if err = state.Put(o, state.Key(state.KeyDependencyQueue), queue); err != nil {
			return state.Transition{}, err
		}
		o.Delete(state.Key(state.KeyDependencyQueued, child[:]))
		payment, found, err := state.Load[rules.PublicPayment](o, state.Key(state.KeyPayment, child[:]))
		if err != nil {
			return state.Transition{}, err
		}
		if !found {
			return state.Transition{}, rules.ErrMissing
		}
		if payment.Settled && payment.Fee.Closed {
			continue
		}
		verified, err := e.verified(payment.Certificate)
		if err != nil {
			return state.Transition{}, err
		}
		transition, err := rules.EvaluateSettlement(o, verified, e.cfg.Schedule)
		if err != nil {
			return state.Transition{}, err
		}
		transition, err = e.enqueue(o, transition)
		if err != nil {
			return state.Transition{}, err
		}
		o.Apply(transition.Changes)
		facts = append(facts, transition.Facts...)
	}
	return state.Transition{Changes: o.Changes(), Facts: facts}, nil
}
