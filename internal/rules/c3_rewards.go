package rules

import (
	"bytes"
	"math/big"
	"sort"
	"utxo/internal/state"
	"utxo/protocol"
)

func (e *directEval) c3Reward(shared, execution uint64) error {
	validators := e.policy.C3.Committee
	if len(validators) == 0 {
		return protocol.ErrAuth
	}
	total := uint64(0)
	validIncluder := false
	for _, v := range validators {
		var err error
		total, err = protocol.Add(total, v.Weight)
		if err != nil {
			return err
		}
		if v.ID == e.policy.Includer {
			validIncluder = true
		}
	}
	if total == 0 || execution > 0 && !validIncluder {
		return protocol.ErrAuth
	}
	type allocation struct {
		id        protocol.Hash
		amount    uint64
		remainder *big.Int
	}
	rows := make([]allocation, 0, len(validators))
	remaining := shared
	for _, v := range validators {
		n := new(big.Int).Mul(new(big.Int).SetUint64(shared), new(big.Int).SetUint64(v.Weight))
		q, r := new(big.Int), new(big.Int)
		q.QuoRem(n, new(big.Int).SetUint64(total), r)
		amount := q.Uint64()
		remaining -= amount
		rows = append(rows, allocation{v.ID, amount, r})
	}
	sort.Slice(rows, func(i, j int) bool {
		n := rows[i].remainder.Cmp(rows[j].remainder)
		if n == 0 {
			return bytes.Compare(rows[i].id[:], rows[j].id[:]) < 0
		}
		return n > 0
	})
	for i := range rows {
		if uint64(i) < remaining {
			rows[i].amount++
		}
		if err := changeAmount(e.o, state.Key(state.KeyReward, rows[i].id[:]), rows[i].amount, false); err != nil {
			return err
		}
	}
	return changeAmount(e.o, state.Key(state.KeyReward, e.policy.Includer[:]), execution, false)
}
