package store

import (
	"bytes"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"utxo/internal/state"
)

func TestStorageContracts(t *testing.T) {
	for _, backend := range []string{"memory", "bbolt"} {
		t.Run(backend, func(t *testing.T) {
			var db Store
			if backend == "memory" {
				db = NewMemory()
			} else {
				b, e := Open(filepath.Join(t.TempDir(), "member.db"), Identity{Network: "test", Role: "member", Node: "0", Schema: 2})
				if e != nil {
					t.Fatal(e)
				}
				db = b
			}
			defer db.Close()
			rollback := errors.New("business rejection")
			if e := db.Update(func(v state.ReadView) ([]state.Change, error) {
				return []state.Change{{Key: []byte("x"), Value: []byte("bad")}}, rollback
			}); !errors.Is(e, rollback) {
				t.Fatal(e)
			}
			db.View(func(v state.ReadView) error {
				_, e := v.Get([]byte("x"))
				if !errors.Is(e, state.ErrNotFound) {
					t.Fatal("partial write")
				}
				return nil
			})
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if e := db.Update(func(v state.ReadView) ([]state.Change, error) {
						b, e := v.Get([]byte("counter"))
						if e != nil && !errors.Is(e, state.ErrNotFound) {
							return nil, e
						}
						n := byte(0)
						if len(b) > 0 {
							n = b[0]
						}
						return []state.Change{{Key: []byte("counter"), Value: []byte{n + 1}}}, nil
					}); e != nil {
						t.Error(e)
					}
				}()
			}
			wg.Wait()
			db.View(func(v state.ReadView) error {
				b, e := v.Get([]byte("counter"))
				if e != nil || !bytes.Equal(b, []byte{20}) {
					t.Fatalf("lost updates %v %v", b, e)
				}
				b[0] = 0
				return nil
			})
			db.View(func(v state.ReadView) error {
				b, _ := v.Get([]byte("counter"))
				if b[0] != 20 {
					t.Fatal("read aliases persistent state")
				}
				return nil
			})
		})
	}
}
func TestP02ReopenIdentityAndAtomicPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "member.db")
	id := Identity{"network", "member", "a", 2}
	db, e := Open(path, id)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Update(func(v state.ReadView) ([]state.Change, error) {
		return []state.Change{{Key: []byte("approval"), Value: []byte("fact")}, {Key: []byte("outbox"), Value: []byte("pending")}}, nil
	}); e != nil {
		t.Fatal(e)
	}
	db.Close()
	wrong := id
	wrong.Node = "other"
	if d, e := Open(path, wrong); e == nil {
		d.Close()
		t.Fatal("identity mismatch")
	}
	db, e = Open(path, id)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.View(func(v state.ReadView) error {
		for _, k := range []string{"approval", "outbox"} {
			if _, e := v.Get([]byte(k)); e != nil {
				t.Fatal(e)
			}
		}
		return nil
	})
}
func TestOverlayIsolation(t *testing.T) {
	base := state.NewOverlay(nil)
	base.Set([]byte("a"), []byte("old"))
	child := state.NewOverlay(base)
	child.Set([]byte("a"), []byte("new"))
	b, _ := base.Get([]byte("a"))
	if string(b) != "old" {
		t.Fatal("child leaked")
	}
	base.Apply(child.Changes())
	b, _ = base.Get([]byte("a"))
	if string(b) != "new" {
		t.Fatal("merge absent")
	}
	child.Delete([]byte("a"))
	if _, e := child.Get([]byte("a")); !errors.Is(e, state.ErrNotFound) {
		t.Fatal("deleted value")
	}
}
