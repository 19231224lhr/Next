package committee

import (
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

func (a *App) FactHeight(id protocol.Hash) (height int64, err error) {
	err = a.db.View(func(v state.ReadView) error {
		loc, found, e := state.Load[factLocation](v, proofKey(id))
		if e != nil {
			return e
		}
		if !found {
			return state.ErrNotFound
		}
		height = loc.Height
		return nil
	})
	return
}
func (a *App) LatestFact(kind protocol.FactKind, key protocol.Hash) (fact protocol.FinalFact, err error) {
	k := new(protocol.Encoder)
	k.U16(uint16(kind))
	k.Fixed(key[:])
	err = a.db.View(func(v state.ReadView) error {
		f, found, e := state.Load[protocol.FinalFact](v, state.Key(70, k.Data()))
		if e != nil {
			return e
		}
		if !found {
			return state.ErrNotFound
		}
		fact = f
		return nil
	})
	return
}
func (e *Engine) Payment(id protocol.SpendFactID) (payment rules.PublicPayment, err error) {
	err = e.db.View(func(v state.ReadView) error {
		p, found, e := state.Load[rules.PublicPayment](v, state.Key(state.KeyPayment, id[:]))
		if e != nil {
			return e
		}
		if !found {
			return state.ErrNotFound
		}
		payment = p
		return nil
	})
	return
}
