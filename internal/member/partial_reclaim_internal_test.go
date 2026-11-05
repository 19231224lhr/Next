package member

import (
	"errors"
	"reflect"
	"testing"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/internal/store"

	"utxo/protocol"
)

func TestInvalidatedApprovalRejectsLateEffects(t *testing.T) {
	cfg := protocol.OrgConfig{}
	fact := protocol.SpendFactID(protocol.Digest("invalidated"))
	for _, action := range []string{"settle", "compensate", "recover"} {
		t.Run(action, func(t *testing.T) {
			db := store.NewMemory()
			defer db.Close()
			m := &Member{db: db, cfg: Config{Organization: cfg}}
			a := state.Approval{Fact: fact, Direct: &protocol.FastTx{}}
			p := LocalProgress{Invalidated: true}
			err := db.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				if err := state.Put(o, state.Key(state.KeyApproval, fact[:]), a); err != nil {
					return nil, err
				}
				if err := state.Put(o, ProgressKey(fact), p); err != nil {
					return nil, err
				}
				return o.Changes(), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			before, _ := store.Scan(db, nil, nil, 1000)
			err = db.Update(func(v state.ReadView) ([]state.Change, error) {
				o := state.NewOverlay(v)
				var err error
				switch action {
				case "settle":
					p.Settled = true
					err = m.finishLocal(o, &a, &p)
				case "compensate":
					err = m.applyRepairEffect(o, protocol.RepairEffect{ParentFact: fact, Amount: 100})
				case "recover":
					err = m.recoverExposedSources(o, []protocol.InputCertificate{{Certificate: protocol.OutputCertificate{Summary: protocol.OutputSummary{Config: cfg.Hash()}, QC: protocol.SpendQC{Fact: fact}}}})
				}
				return o.Changes(), err
			})
			if !errors.Is(err, rules.ErrAccounting) {
				t.Fatal("late effect accepted", err)
			}
			after, _ := store.Scan(db, nil, nil, 1000)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rejected effect changed state")
			}
		})
	}
}
