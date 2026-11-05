package member

import (
	"errors"
	"testing"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

func TestDirectStatusesPreserveOrderAndUnknown(t *testing.T) {
	db := store.NewMemory()
	m := &Member{db: db}
	var f protocol.SpendFactID
	f[0] = 1
	if err := db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if e := state.Put(o, state.Key(state.KeyObserved, f[:]), true); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := m.DirectStatuses([]protocol.SpendFactID{f, {}, f})
	if err != nil || len(got) != 3 || !got[0].Observed || got[1] != (DirectStatus{}) || got[2] != got[0] {
		t.Fatal(got, err)
	}
	if _, e := m.DirectStatuses(nil); e == nil {
		t.Fatal("empty accepted")
	}
	if _, e := m.DirectStatuses(make([]protocol.SpendFactID, 129)); e == nil {
		t.Fatal("oversized accepted")
	}
	db.Close()
	if _, e := m.DirectStatuses([]protocol.SpendFactID{f}); !errors.Is(e, store.ErrClosed) {
		t.Fatal(e)
	}
}
