package gateway

import (
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/state"
	"utxo/protocol"
)

func (r *Relay) ObserveBlock(config protocol.Hash) blockfollow.Prepare {
	return func(b finality.VerifiedBlock) (blockfollow.Apply, error) {
		var facts []protocol.SpendFactID
		for _, t := range b.Transactions() {
			if t.Code != 0 || len(t.Data) == 0 {
				continue
			}
			result, err := protocol.DecodeExecution(t.Data)
			if err != nil {
				return nil, err
			}
			if !result.Applied {
				continue
			}
			p, err := protocol.DecodeDirectSubmission(t.Bytes)
			if err != nil {
				continue
			}
			if p.Tx.Body.Config == config {
				facts = append(facts, p.Authorization.Fact)
			}
		}
		return func(o *state.Overlay) error {
			for _, fact := range facts {
				// Also covers block arrival before background outbox persistence.
				r.forgetInstall(fact)
				if err := state.Put(o, state.Key(state.KeyObserved, fact[:]), true); err != nil {
					return err
				}
				o.Apply([]state.Change{{Key: state.Key(state.KeyOutbox, fact[:]), Delete: true}})
			}
			return nil
		}, nil
	}
}
