package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

type directLedgerAudit struct {
	CAL, FUEL, Gap, Rewards, Burned uint64
	Payments, Closed                int
	StateHash                       string
}

func auditDirectLedger(v state.ReadView, n cfg.Network) (result directLedgerAudit, err error) {
	add := func(sum *uint64, x uint64) error { var err error; *sum, err = protocol.Add(*sum, x); return err }
	var initialCAL, initialFUEL uint64
	for _, o := range n.Genesis.Outputs {
		if err = add(&initialCAL, o.Output.Amount); err != nil {
			return result, err
		}
	}
	for _, a := range n.Accounts {
		target := &initialCAL
		if a.Asset == protocol.AssetFUEL {
			target = &initialFUEL
		}
		if err = add(target, a.Balance); err != nil {
			return result, err
		}
	}
	scanner := v.(state.ScanView)
	hash := sha256.New()
	var cursor []byte
	for {
		entries, err := scanner.Scan(nil, cursor, 512)
		if err != nil {
			return result, err
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			framed := new(protocol.Encoder)
			framed.Bytes(entry.Key)
			framed.Bytes(entry.Value)
			hash.Write(framed.Data())
			cursor = entry.Key
			d := protocol.NewDecoder(entry.Key)
			if d.U16() != 2 {
				continue
			}
			kind := d.U8()
			switch kind {
			case state.KeyCreation:
				var id protocol.OutputID
				copy(id[:], d.Bytes(32))
				instance := d.Bytes(1)
				if len(instance) != 1 || d.Done() != nil {
					return result, protocol.ErrEncoding
				}
				var creation state.Creation
				if err = json.Unmarshal(entry.Value, &creation); err != nil {
					return result, err
				}
				spent, _, err := state.Load[state.Spend](v, rules.DirectSpendKey(id, instance[0]))
				if err != nil {
					return result, err
				}
				if spent.Consumed == (protocol.SpendFactID{}) {
					if err = add(&result.CAL, creation.Output.Amount); err != nil {
						return result, err
					}
				}
			case state.KeyAccount:
				d.Bytes(32)
				asset := d.Bytes(1)
				if len(asset) != 1 || d.Done() != nil {
					return result, protocol.ErrEncoding
				}
				var balance uint64
				if err = json.Unmarshal(entry.Value, &balance); err != nil {
					return result, err
				}
				target := &result.CAL
				if protocol.Asset(asset[0]) == protocol.AssetFUEL {
					target = &result.FUEL
				}
				if err = add(target, balance); err != nil {
					return result, err
				}
			}
		}
	}
	result.StateHash = hex.EncodeToString(hash.Sum(nil))
	payments, err := records[rules.DirectPaymentState](v, 103)
	if err != nil {
		return result, err
	}
	result.Payments = len(payments)
	for _, payment := range payments {
		if err = payment.Fee.Validate(); err != nil {
			return result, err
		}
		if err = add(&result.FUEL, payment.Fee.Held); err != nil {
			return result, err
		}
		if payment.Fee.Closed {
			result.Closed++
		}
	}
	rewards, err := records[uint64](v, state.KeyReward)
	if err != nil {
		return result, err
	}
	for _, reward := range rewards {
		if err = add(&result.Rewards, reward); err != nil {
			return result, err
		}
	}
	result.Burned, _, err = state.Load[uint64](v, state.Key(state.KeyBurned))
	if err != nil {
		return result, err
	}
	if err = add(&result.FUEL, result.Rewards); err != nil {
		return result, err
	}
	if err = add(&result.FUEL, result.Burned); err != nil {
		return result, err
	}
	result.Gap, _, err = state.Load[uint64](v, rules.DirectGapKey())
	if err != nil {
		return result, err
	}
	expectedCAL, err := protocol.Add(initialCAL, result.Gap)
	if err != nil {
		return result, err
	}
	if result.CAL != expectedCAL || result.FUEL != initialFUEL {
		return result, fmt.Errorf("v3 conservation mismatch CAL %d/%d FUEL %d/%d", result.CAL, expectedCAL, result.FUEL, initialFUEL)
	}
	return result, nil
}
