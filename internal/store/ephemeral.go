package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/btree"
	bolt "go.etcd.io/bbolt"
	"utxo/internal/state"
)

var ErrAuditSnapshot = errors.New("ephemeral audit snapshot or unfinished experiment; use a fresh directory")
var auditOnlyKey = []byte("audit-only")

// Ephemeral keeps experimental node state in memory. It has atomic updates
// and ordered scans, but no crash recovery. Close exports a final audit snapshot;
// that export is outside the payment benchmark and is never used to resume.
type Ephemeral struct {
	mu       sync.RWMutex
	view     orderedView
	path     string
	identity Identity
	closed   bool
	closeErr error
}
type orderedView struct{ tree *btree.BTreeG[state.Entry] }

func OpenEphemeral(path string, id Identity) (*Ephemeral, error) {
	if id.Network == "" || id.Role == "" || id.Node == "" || id.Schema == 0 {
		return nil, ErrIdentity
	}
	if _, e := os.Stat(path); e == nil {
		return nil, fmt.Errorf("experiment snapshot already exists: %s", path)
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	// Reserve the experiment path; an interrupted run must use a fresh directory.
	f, e := os.OpenFile(path+".pending", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	if e = f.Close(); e != nil {
		return nil, e
	}
	tree := btree.NewG[state.Entry](32, func(a, b state.Entry) bool { return bytes.Compare(a.Key, b.Key) < 0 })
	return &Ephemeral{view: orderedView{tree}, path: path, identity: id}, nil
}
func (v orderedView) Get(k []byte) ([]byte, error) {
	e, ok := v.tree.Get(state.Entry{Key: k})
	if !ok {
		return nil, state.ErrNotFound
	}
	return bytes.Clone(e.Value), nil
}
func (v orderedView) Scan(prefix, after []byte, limit int) ([]state.Entry, error) {
	start := prefix
	if bytes.Compare(after, start) > 0 {
		start = after
	}
	result := make([]state.Entry, 0, limit)
	v.tree.AscendGreaterOrEqual(state.Entry{Key: start}, func(e state.Entry) bool {
		if !bytes.HasPrefix(e.Key, prefix) {
			return false
		}
		if bytes.Compare(e.Key, after) <= 0 {
			return true
		}
		result = append(result, state.Entry{Key: bytes.Clone(e.Key), Value: bytes.Clone(e.Value)})
		return len(result) < limit
	})
	return result, nil
}
func (m *Ephemeral) View(fn func(state.ReadView) error) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return ErrClosed
	}
	return fn(m.view)
}
func (m *Ephemeral) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	changes, e := fn(m.view)
	if e != nil {
		return e
	}
	for _, c := range changes {
		if len(c.Key) == 0 {
			return errors.New("empty state key")
		}
	}
	for _, c := range changes {
		if c.Delete {
			m.view.tree.Delete(state.Entry{Key: c.Key})
		} else {
			m.view.tree.ReplaceOrInsert(state.Entry{Key: bytes.Clone(c.Key), Value: bytes.Clone(c.Value)})
		}
	}
	return nil
}
func (m *Ephemeral) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.closeErr
	}
	m.closed = true
	m.closeErr = m.export()
	return m.closeErr
}
func (m *Ephemeral) export() error {
	db, e := OpenNoSync(m.path+".pending", m.identity)
	if e != nil {
		return e
	}
	// Bound export memory, and preserve ordered keys for the offline bbolt file.
	batch := make([]state.Change, 0, 4096)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		e := db.Update(func(state.ReadView) ([]state.Change, error) { return batch, nil })
		batch = batch[:0]
		return e
	}
	m.view.tree.Ascend(func(row state.Entry) bool {
		batch = append(batch, state.Change{Key: row.Key, Value: row.Value})
		if len(batch) == cap(batch) {
			e = flush()
		}
		return e == nil
	})
	if e == nil {
		e = flush()
	}
	if e == nil {
		e = db.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(metaBucket).Put(auditOnlyKey, []byte{1}) })
	}
	if e == nil {
		e = db.db.Sync()
	}
	e = errors.Join(e, db.Close())
	if e != nil {
		return e
	}
	return os.Rename(m.path+".pending", m.path)
}
