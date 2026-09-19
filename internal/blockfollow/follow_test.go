package blockfollow_test

import (
	"errors"
	"testing"
	"utxo/finality"
	"utxo/internal/blockfollow"
	"utxo/internal/state"
	"utxo/internal/store"
	"utxo/internal/testkit"
)

func TestCursorAndApplicationCommitTogether(t *testing.T) {
	db := store.NewMemory()
	defer db.Close()
	trust, p := testkit.Block("cursor", 1, nil, nil, nil)
	b, err := finality.VerifyBlock(trust, p)
	if err != nil {
		t.Fatal(err)
	}
	key := state.Key(121)
	fail := errors.New("injected write failure")
	if err = blockfollow.Commit(db, b, func(finality.VerifiedBlock) (blockfollow.Apply, error) {
		return func(o *state.Overlay) error { o.Set(key, []byte{1}); return fail }, nil
	}); !errors.Is(err, fail) {
		t.Fatal(err)
	}
	height, err := blockfollow.Height(db)
	if err != nil || height != 0 {
		t.Fatal("cursor advanced despite rollback")
	}
	err = db.View(func(v state.ReadView) error { _, err := v.Get(key); return err })
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatal("failed application leaked state")
	}
	calls := 0
	apply := func(finality.VerifiedBlock) (blockfollow.Apply, error) {
		return func(o *state.Overlay) error { calls++; o.Set(key, []byte{2}); return nil }, nil
	}
	for i := 0; i < 2; i++ {
		if err = blockfollow.Commit(db, b, apply); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("duplicate block reapplied")
	}
	trust, p = testkit.Block("cursor", 3, b.Hash(), nil, nil)
	b, err = finality.VerifyBlock(trust, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = blockfollow.Commit(db, b, apply); err == nil {
		t.Fatal("accepted skipped height")
	}
}
