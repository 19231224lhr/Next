package store

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"utxo/internal/state"
)

func TestFreshExperimentGrowth(t *testing.T) {
	for _, noSync := range []bool{false, true} {
		name := "synchronous"
		if noSync {
			name = "experiment"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			id := Identity{"test", "member", "growth", 1}
			db, err := openBolt(path, id, noSync)
			if err != nil {
				t.Fatal(err)
			}
			if db.db.NoSync != noSync || db.db.NoGrowSync != (noSync && runtime.GOOS == "darwin") {
				db.Close()
				t.Fatal("experiment must disable both transaction and growth synchronization; normal stores must retain both")
			}
			wantPage := os.Getpagesize()
			if got := db.db.Info().PageSize; got != wantPage {
				db.Close()
				t.Fatalf("page size = %d; want %d", got, wantPage)
			}
			value := bytes.Repeat([]byte{37}, 17<<20)
			if err := db.Update(func(state.ReadView) ([]state.Change, error) {
				return []state.Change{{Key: []byte("large"), Value: value}}, nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = openBolt(path, id, noSync)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.View(func(v state.ReadView) error {
				got, e := v.Get([]byte("large"))
				if e != nil {
					return e
				}
				if !bytes.Equal(got, value) {
					t.Error("expanded store lost committed bytes on clean reopen")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
