package wallet

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
	"utxo/protocol"
)

func TestReceiveDirectPrimesOwnedFullCoin(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	w, req := directBytesFixture(t, db)
	f := testkit.NewFixture("wallet-saved-bytes", "a", 1)
	var vector protocol.AdmissionVector
	for _, a := range req.Tx.Body.Admission {
		cap := uint64(10000000)
		if a.Key.Kind == protocol.ResourceCAL {
			cap = req.Tx.Body.Outputs[0].Amount
		}
		vector = append(vector, protocol.Allocation{Key: a.Key, Cap: cap})
	}
	cert := protocol.OutputCertificate{Summary: protocol.SummaryFor(req.Tx, vector)}
	cert.QC.Fact = cert.Summary.Fact()
	for i := 0; i < 3; i++ {
		cert.QC.Votes = append(cert.QC.Votes, protocol.SignSpend(cert.QC.Fact, uint16(i), f.Keys[i]))
	}
	out := req.Tx.Body.Outputs[0]
	id := cert.Summary.OutputID(0)
	key := DirectCoinKey(id, 0)
	w.db = directBytesFailingStore{Store: db, err: errors.New("receive commit failed")}
	if err := w.ReceiveDirect(out, cert, 0); err == nil {
		t.Fatal("commit failure accepted")
	}
	if len(w.coinStatus.entries) != 0 {
		t.Fatal("primed before commit")
	}
	w.db = db
	if err := w.ReceiveDirect(out, cert, 0); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	_ = db.View(func(v state.ReadView) error { var e error; raw, e = v.Get(key); return e })
	expected := DirectCoin{}
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	got, hit := w.coinStatus.lookupCoin(key, raw)
	if !hit || !reflect.DeepEqual(got, expected) {
		t.Fatal("not primed")
	}
	got.Certificate.QC.Votes[0].Signature[0] ^= 1
	got.Certificate.Summary.Outputs[0].Amount++
	got.Certificate.Summary.Admission[0].Cap++
	got.Certificate.Summary.Grants[0][0] ^= 1
	cert.QC.Votes[0].Signature[0] ^= 1
	again, found, err := w.ReadDirectCoin(id, 0)
	if err != nil || !found || !reflect.DeepEqual(again, expected) {
		t.Fatal("cache aliases caller", err)
	}
	// A final/recovered update must win, including after an idempotent receive.
	expected.Final = protocol.Hash{1}
	expected.Recovered = true
	err = db.Update(func(v state.ReadView) ([]state.Change, error) {
		o := state.NewOverlay(v)
		if e := state.Put(o, key, expected); e != nil {
			return nil, e
		}
		return o.Changes(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cert.QC.Votes[0].Signature[0] ^= 1
	if err = w.ReceiveDirect(out, cert, 0); err != nil {
		t.Fatal(err)
	}
	again, found, err = w.ReadDirectCoin(id, 0)
	if err != nil || !found || !reflect.DeepEqual(again, expected) {
		t.Fatal("stale cached coin", err)
	}
}
