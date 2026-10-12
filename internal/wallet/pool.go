package wallet

import (
	"context"
	"errors"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

func PoolReceiptKey(pool protocol.Hash) []byte { return state.Key(162, pool[:]) }
func poolRequestKey(pool protocol.Hash, previous protocol.Hash) []byte {
	return state.Key(163, pool[:], previous[:])
}
func poolCertificateIdentity(c protocol.PoolCertificate) protocol.Hash {
	raw, _ := c.MarshalBinary()
	return protocol.Digest("INTERNAL_POOL_CERTIFICATE_V1", raw)
}

// ReceivePool checks the full organization QC and saves an internal receipt.
// This does not create an ordinary publicly spendable DirectCoin.
func (w *Wallet) ReceivePool(c protocol.PoolCertificate) error {
	a := c.Action
	org, ok := w.orgs[a.Config]
	if !ok || c.Verify(org) != nil || a.Kind != protocol.PoolTransfer || a.Network != w.network || a.To.Owner != w.owner || org.Org != w.bound {
		return protocol.ErrAuth
	}
	w.poolMu.Lock()
	defer w.poolMu.Unlock()
	var previous protocol.PoolCertificate
	var found bool
	if err := w.db.View(func(v state.ReadView) (e error) {
		previous, found, e = state.Load[protocol.PoolCertificate](v, PoolReceiptKey(a.Pool))
		return e
	}); err != nil {
		return err
	}
	if found {
		if previous.Action.Sequence > a.Sequence || poolCertificateIdentity(previous) == poolCertificateIdentity(c) {
			return nil
		}
		if previous.Action.Sequence == a.Sequence && previous.Action.ID() != a.ID() {
			return protocol.ErrAuth
		}
	}
	if err := w.db.Update(func(v state.ReadView) ([]state.Change, error) {
		old, found, e := state.Load[protocol.PoolCertificate](v, PoolReceiptKey(a.Pool))
		if e != nil {
			return nil, e
		}
		if found {
			if old.Action.Sequence > a.Sequence {
				return nil, nil
			}
			if old.Action.Sequence == a.Sequence {
				if old.Action.ID() != a.ID() {
					return nil, protocol.ErrAuth
				}
				if poolCertificateIdentity(old) == poolCertificateIdentity(c) {
					return nil, nil
				}
			}
		}
		o := state.NewOverlay(v)
		if e = state.Put(o, PoolReceiptKey(a.Pool), c); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	}); err != nil {
		return err
	}
	delete(w.poolReady, a.Pool)
	return nil
}

// PoolInstaller is a configured member endpoint that durably installs a full QC.
type PoolInstaller interface {
	InstallPool(context.Context, protocol.PoolCertificate) error
}

// CompletePool overlaps receipt persistence and two distinct member installs.
// Readiness is kept only in this wallet process for the exact receipt at its head.
func (w *Wallet) CompletePool(ctx context.Context, c protocol.PoolCertificate, members [4]PoolInstaller) error {
	return w.completePool(ctx, c, members, false)
}

// CompletePoolSerial has the same completion and readiness boundary for benchmarks.
func (w *Wallet) CompletePoolSerial(ctx context.Context, c protocol.PoolCertificate, members [4]PoolInstaller) error {
	return w.completePool(ctx, c, members, true)
}

func (w *Wallet) completePool(ctx context.Context, c protocol.PoolCertificate, members [4]PoolInstaller, serial bool) error {
	var receiveErr error
	if serial {
		receiveErr = w.ReceivePool(c)
		if receiveErr != nil {
			return receiveErr
		}
	} else {
		received := make(chan error, 1)
		go func() { received <- w.ReceivePool(c) }()
		installErr := installPoolMembers(ctx, c, members)
		receiveErr = <-received
		if receiveErr != nil {
			return receiveErr
		}
		if installErr != nil {
			return installErr
		}
	}
	if serial {
		if err := installPoolMembers(ctx, c, members); err != nil {
			return err
		}
	}
	w.poolMu.Lock()
	defer w.poolMu.Unlock()
	var current protocol.PoolCertificate
	var found bool
	if err := w.db.View(func(v state.ReadView) (e error) {
		current, found, e = state.Load[protocol.PoolCertificate](v, PoolReceiptKey(c.Action.Pool))
		return e
	}); err != nil {
		return err
	}
	if !found || poolCertificateIdentity(current) != poolCertificateIdentity(c) {
		return protocol.ErrAuth
	}
	if w.poolReady == nil {
		w.poolReady = make(map[protocol.Hash]protocol.Hash)
	}
	w.poolReady[c.Action.Pool] = poolCertificateIdentity(c)
	return nil
}

func installPoolMembers(ctx context.Context, c protocol.PoolCertificate, members [4]PoolInstaller) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 4)
	for _, m := range members {
		go func(m PoolInstaller) {
			if m == nil {
				results <- protocol.ErrAuth
				return
			}
			results <- m.InstallPool(ctx, c)
		}(m)
	}
	acks := 0
	var failures []error
	for range members {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-results:
			if err != nil {
				failures = append(failures, err)
				continue
			}
			acks++
			if acks == 2 {
				return nil
			}
		}
	}
	return errors.Join(append([]error{errors.New("pool certificate lacks two durable copies; retry the same signed request")}, failures...)...)
}

// SavePoolRequest fixes one authorized action per predecessor before any RPC.
func (w *Wallet) SavePoolRequest(r protocol.PoolRequest) error {
	a := r.Action
	org, ok := w.orgs[a.Config]
	if !ok || a.Verify(org) != nil || a.From != w.owner || a.Network != w.network || org.Org != w.bound {
		return protocol.ErrAuth
	}
	if a.Kind != protocol.PoolDeposit {
		if r.Parent == nil || r.Parent.Verify(org) != nil {
			return protocol.ErrAuth
		}
		p := r.Parent.Action
		if p.Kind == protocol.PoolExit || p.Pool != a.Pool || p.To.Owner != w.owner || p.Amount != a.Amount || p.Sequence+1 != a.Sequence || p.ID() != a.Previous {
			return protocol.ErrAuth
		}
	} else if r.Parent != nil {
		return protocol.ErrRule
	}
	w.poolMu.Lock()
	defer w.poolMu.Unlock()
	return w.db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if r.Parent != nil && r.Parent.Action.Kind == protocol.PoolTransfer {
			current, found, e := state.Load[protocol.PoolCertificate](o, PoolReceiptKey(a.Pool))
			if e != nil {
				return nil, e
			}
			identity := poolCertificateIdentity(current)
			if !found || identity != poolCertificateIdentity(*r.Parent) || w.poolReady[a.Pool] != identity {
				return nil, protocol.ErrAuth
			}
		}
		if e := rules.ValidatePoolRecipient(o, a, org); e != nil {
			return nil, e
		}
		key := poolRequestKey(a.Pool, a.Previous)
		old, found, e := state.Load[protocol.PoolRequest](o, key)
		if e != nil {
			return nil, e
		}
		if found {
			if old.Action.ID() != a.ID() {
				return nil, protocol.ErrAuth
			}
			return nil, nil
		}
		if a.Kind == protocol.PoolDeposit {
			coin, known, err := state.Load[DirectCoin](o, DirectCoinKey(a.Input, 0))
			if err != nil {
				return nil, err
			}
			if !known {
				if origin, ok := w.origins[a.Input]; ok {
					coin = DirectCoin{Output: origin.Output, Final: origin.Fact}
					known = true
				}
			}
			if !known || coin.Recovered || coin.Final != a.Evidence || coin.Output.Asset != protocol.AssetCAL || coin.Output.Amount != a.Amount || coin.Output.Recipient != a.To {
				return nil, protocol.ErrAuth
			}
			k := state.Key(state.KeyWalletSpend, a.Input[:], []byte{0})
			prior, exists, e := state.Load[protocol.TxID](o, k)
			if e != nil {
				return nil, e
			}
			id := protocol.TxID(a.ID())
			if exists && prior != id {
				return nil, protocol.ErrAuth
			}
			if e = state.Put(o, k, id); e != nil {
				return nil, e
			}
		}
		if e = state.Put(o, key, r); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	})
}
