package main

import (
	"os"
	"path/filepath"
	"testing"
	"utxo/internal/store"
)

func TestCommitteeStoreExperimentIsExplicitAndFresh(t *testing.T) {
	id := store.Identity{Network: "test", Role: "committee", Node: "0", Schema: 4}
	t.Setenv("UTXO_EXPERIMENT_COMMITTEE_MEMORY", "")
	t.Setenv("UTXO_EXPERIMENT_MEM_BLOCKSTORE", "1")
	db, err := openCommitteeStore(filepath.Join(t.TempDir(), "default.db"), id, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := db.(*store.Bolt); !ok {
		t.Fatal("default must remain durable bbolt")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UTXO_EXPERIMENT_COMMITTEE_MEMORY", "1")
	if db, err = openCommitteeStore(filepath.Join(t.TempDir(), "legacy.db"), id, false); err == nil {
		db.Close()
		t.Fatal("legacy protocol accepted experimental store")
	}
	t.Setenv("UTXO_EXPERIMENT_MEM_BLOCKSTORE", "")
	if db, err = openCommitteeStore(filepath.Join(t.TempDir(), "mixed.db"), id, true); err == nil {
		db.Close()
		t.Fatal("memory app with resumable block storage")
	}
	t.Setenv("UTXO_EXPERIMENT_MEM_BLOCKSTORE", "1")
	path := filepath.Join(t.TempDir(), "committee.db")
	db, err = openCommitteeStore(path, id, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := db.(*store.Ephemeral); !ok {
		t.Fatal("experiment did not use memory")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("unexpected live audit snapshot", err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if db, err = openCommitteeStore(path, id, true); err == nil {
		db.Close()
		t.Fatal("reused an old experiment directory")
	}
}
