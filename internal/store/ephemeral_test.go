package store

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"utxo/internal/state"
)

func TestEphemeralAtomicOwnershipAndAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "member.db")
	id := Identity{"n", "member", "0", 1}
	db, e := OpenEphemeral(path, id)
	if e != nil {
		t.Fatal(e)
	}
	k, v := []byte("p/b"), []byte("two")
	if e = db.Update(func(state.ReadView) ([]state.Change, error) {
		return []state.Change{{Key: []byte("p/a"), Value: []byte("one")}, {Key: k, Value: v}, {Key: []byte("q/a"), Value: []byte("other")}}, nil
	}); e != nil {
		t.Fatal(e)
	}
	k[0] = 'z'
	v[0] = 'z'
	rejected := errors.New("rejected")
	if e = db.Update(func(state.ReadView) ([]state.Change, error) {
		return []state.Change{{Key: []byte("p/a"), Delete: true}}, rejected
	}); !errors.Is(e, rejected) {
		t.Fatal(e)
	}
	if e = db.Update(func(state.ReadView) ([]state.Change, error) {
		return []state.Change{{Key: []byte("p/a"), Delete: true}, {Key: nil, Value: []byte("bad")}}, nil
	}); e == nil {
		t.Fatal("invalid batch accepted")
	}
	rows, e := Scan(db, []byte("p/"), []byte("p/a"), 1)
	if e != nil || len(rows) != 1 || string(rows[0].Value) != "two" {
		t.Fatalf("scan: %v %v", rows, e)
	}
	rows[0].Value[0] = 'z'
	if e = db.View(func(v state.ReadView) error {
		got, e := v.Get([]byte("p/b"))
		if e != nil || string(got) != "two" {
			t.Fatal(string(got), e)
		}
		got[0] = 'z'
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("audit snapshot must not exist while running", e)
	}
	if _, e = OpenEphemeral(path, id); e == nil {
		t.Fatal("opened same live experiment")
	}
	if b, e := OpenNoSync(path, id); e == nil {
		b.Close()
		t.Fatal("ordinary mode ignored active ephemeral marker")
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	if e = Inspect(path, func(v state.ReadView) error {
		a, e := v.Get([]byte("p/a"))
		if e != nil || !bytes.Equal(a, []byte("one")) {
			t.Fatal(a, e)
		}
		b, e := v.Get([]byte("p/b"))
		if string(b) != "two" {
			t.Fatal(string(b))
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = OpenEphemeral(path, id); e == nil {
		t.Fatal("silently restarted an old experiment")
	}
	if b, e := OpenNoSync(path, id); e == nil {
		b.Close()
		t.Fatal("audit snapshot accepted as normal restart")
	}
	if e = db.Update(func(state.ReadView) ([]state.Change, error) { return nil, nil }); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
}
func TestEphemeralConflictingWriters(t *testing.T) {
	db, e := OpenEphemeral(filepath.Join(t.TempDir(), "m.db"), Identity{"n", "member", "0", 1})
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var wg sync.WaitGroup
	wins := 0
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = db.Update(func(v state.ReadView) ([]state.Change, error) {
				_, e := v.Get([]byte("input"))
				if e == nil {
					return nil, errors.New("spent")
				}
				if !errors.Is(e, state.ErrNotFound) {
					return nil, e
				}
				wins++
				return []state.Change{{Key: []byte("input"), Value: []byte("spent")}}, nil
			})
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatal(wins)
	}
}

func TestEphemeralPagedExportDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "member.db")
	db, e := OpenEphemeral(path, Identity{"n", "member", "0", 1})
	if e != nil {
		t.Fatal(e)
	}
	cs := make([]state.Change, 10003)
	for i := range cs {
		cs[i] = state.Change{Key: []byte(fmt.Sprintf("p/%06d", i)), Value: []byte(fmt.Sprintf("v%d", i))}
	}
	if e = db.Update(func(state.ReadView) ([]state.Change, error) { return cs, nil }); e != nil {
		t.Fatal(e)
	}
	if e = db.Update(func(state.ReadView) ([]state.Change, error) {
		return []state.Change{{Key: cs[123].Key, Delete: true}, {Key: cs[5555].Key, Value: []byte("replaced")}}, nil
	}); e != nil {
		t.Fatal(e)
	}
	digest := func(v state.ReadView) []byte {
		t.Helper()
		h := sha256.New()
		scan := v.(state.ScanView)
		var after []byte
		count := 0
		for {
			rows, e := scan.Scan(nil, after, 73)
			if e != nil {
				t.Fatal(e)
			}
			if len(rows) == 0 {
				break
			}
			for _, r := range rows {
				fmt.Fprintf(h, "%d:", len(r.Key))
				h.Write(r.Key)
				fmt.Fprintf(h, "%d:", len(r.Value))
				h.Write(r.Value)
				count++
			}
			after = rows[len(rows)-1].Key
		}
		if count != 10002 {
			t.Fatal(count)
		}
		return h.Sum(nil)
	}
	var before []byte
	if e = db.View(func(v state.ReadView) error { before = digest(v); return nil }); e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	if e = Inspect(path, func(v state.ReadView) error {
		if !bytes.Equal(before, digest(v)) {
			t.Fatal("export differs from final live state")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
