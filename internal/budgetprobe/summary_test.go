package budgetprobe

import (
	"net/http/httptest"
	"testing"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/protocol"
)

type keyOnlyView struct{ state.ReadView }
type keyOnlyStore struct{ store.Store }

func (s keyOnlyStore) View(fn func(state.ReadView) error) error {
	return s.Store.View(func(v state.ReadView) error { return fn(keyOnlyView{v}) })
}

func TestSummaryBudgetDoesNotScanObligationHistory(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	h := Handler(keyOnlyStore{db}, nil, protocol.OrgConfig{}, 0, nil)
	req := httptest.NewRequest("GET", "/debug/budget?summary=1", nil)
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
}
