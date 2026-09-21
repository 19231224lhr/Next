package main

import (
	"bytes"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	cmtcfg "github.com/cometbft/cometbft/config"
)

func TestDirectDatabaseReopensStandardStore(t *testing.T) {
	c := cmtcfg.DefaultConfig().SetRoot(t.TempDir())
	ctx := &cmtcfg.DBContext{ID: "blockstore", Config: c}
	db, err := cmtcfg.DefaultDBProvider(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SetSync([]byte("old"), []byte("retained")); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = directDBProvider(ctx)
	if err != nil {
		t.Fatal(err)
	}
	batch := db.NewBatch()
	// This crosses the old 4MiB large-batch threshold but fits the 64MiB
	// memtable. Reopen with stock options to verify journal compatibility.
	large := bytes.Repeat([]byte("synced"), 1<<20)
	if err = batch.Set([]byte("new"), large); err != nil {
		t.Fatal(err)
	}
	if err = batch.WriteSync(); err != nil {
		t.Fatal(err)
	}
	if err = batch.Close(); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = cmtcfg.DefaultDBProvider(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for key, want := range map[string][]byte{"old": []byte("retained"), "new": large} {
		got, err := db.Get([]byte(key))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s: got %d bytes, expected %d: %v", key, len(got), len(want), err)
		}
	}
}

func TestDirectDatabasePreservesConfiguredBackend(t *testing.T) {
	c := cmtcfg.DefaultConfig().SetRoot(t.TempDir())
	c.DBBackend = string(dbm.MemDBBackend)
	db, err := directDBProvider(&cmtcfg.DBContext{ID: "blockstore", Config: c})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, ok := db.(*dbm.MemDB); !ok {
		t.Fatalf("overrode configured backend: %T", db)
	}
}

func TestDirectDatabaseMemoryBlockstoreIsOptIn(t *testing.T) {
	c := cmtcfg.DefaultConfig().SetRoot(t.TempDir())
	ctx := &cmtcfg.DBContext{ID: "blockstore", Config: c}

	t.Setenv("UTXO_EXPERIMENT_MEM_BLOCKSTORE", "1")
	db, err := directDBProvider(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, ok := db.(*dbm.MemDB); !ok {
		t.Fatalf("memory blockstore is not enabled: %T", db)
	}
	if err := db.SetSync([]byte("height"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	got, err := db.Get([]byte("height"))
	if err != nil || string(got) != "1" {
		t.Fatalf("memory blockstore readback: %q, %v", got, err)
	}
}

func TestDirectDatabaseMemoryModeUsesIndependentCometStores(t *testing.T) {
	c := cmtcfg.DefaultConfig().SetRoot(t.TempDir())
	t.Setenv("UTXO_EXPERIMENT_MEM_BLOCKSTORE", "1")

	stores := make(map[string]dbm.DB, 3)
	for _, id := range []string{"blockstore", "state", "evidence"} {
		db, err := directDBProvider(&cmtcfg.DBContext{ID: id, Config: c})
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, ok := db.(*dbm.MemDB); !ok {
			t.Fatalf("memory mode did not replace %s database: %T", id, db)
		}
		stores[id] = db
	}

	if err := stores["blockstore"].SetSync([]byte("block-only"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if got, err := stores["state"].Get([]byte("block-only")); err != nil || got != nil {
		t.Fatalf("blockstore data leaked into state: %q, %v", got, err)
	}
	if err := stores["evidence"].SetSync([]byte("evidence-only"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if got, err := stores["state"].Get([]byte("evidence-only")); err != nil || got != nil {
		t.Fatalf("evidence data leaked into state: %q, %v", got, err)
	}
}

func TestDirectDatabaseMemoryModeLeavesUnknownStorePersistent(t *testing.T) {
	c := cmtcfg.DefaultConfig().SetRoot(t.TempDir())
	t.Setenv("UTXO_EXPERIMENT_MEM_BLOCKSTORE", "1")
	db, err := directDBProvider(&cmtcfg.DBContext{ID: "application", Config: c})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, ok := db.(*dbm.MemDB); ok {
		t.Fatal("memory mode must not replace unknown databases")
	}
}
