package store

import (
	"bytes"
	"encoding/json"
	"errors"
	bolt "go.etcd.io/bbolt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"utxo/internal/requesttrace"
	"utxo/internal/state"
)

var errNoWrites = errors.New("no logical changes")

var dataBucket = []byte("state.v2")
var metaBucket = []byte("identity.v2")

type Bolt struct {
	db        *bolt.DB
	writeMu   sync.Mutex
	uncertain bool
	trace     bool
}
type boltView struct{ b *bolt.Bucket }

func (v boltView) Get(k []byte) ([]byte, error) {
	b := v.b.Get(k)
	if b == nil {
		return nil, state.ErrNotFound
	}
	return bytes.Clone(b), nil
}
func Open(path string, id Identity) (*Bolt, error) {
	if id.Network == "" || id.Role == "" || id.Node == "" || id.Schema == 0 {
		return nil, ErrIdentity
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	db, e := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second, InitialMmapSize: 16 << 20})
	if e != nil {
		return nil, e
	}
	expected, _ := json.Marshal(id)
	e = db.Update(func(tx *bolt.Tx) error {
		meta, e := tx.CreateBucketIfNotExists(metaBucket)
		if e != nil {
			return e
		}
		previous := meta.Get([]byte("identity"))
		if previous != nil && !bytes.Equal(previous, expected) {
			return ErrIdentity
		}
		if previous == nil {
			if e := meta.Put([]byte("identity"), expected); e != nil {
				return e
			}
		}
		_, e = tx.CreateBucketIfNotExists(dataBucket)
		return e
	})
	if e != nil {
		db.Close()
		return nil, e
	}
	return &Bolt{db: db, trace: (id.Role == "gateway" || id.Role == "committee") && requesttrace.Consensus != nil}, nil
}
func (b *Bolt) View(fn func(state.ReadView) error) error {
	return b.db.View(func(tx *bolt.Tx) error { return fn(boltView{tx.Bucket(dataBucket)}) })
}
func (b *Bolt) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	var requested, locked, callback, evaluated, writes int64
	var before bolt.Stats
	if b.trace {
		requested = time.Now().UnixNano()
	}
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if b.trace {
		locked = time.Now().UnixNano()
		before = b.db.Stats()
	}
	if b.uncertain {
		return ErrUncertain
	}
	var businessErr error
	err := b.db.Update(func(tx *bolt.Tx) error {
		if b.trace {
			callback = time.Now().UnixNano()
		}
		bucket := tx.Bucket(dataBucket)
		cs, e := fn(boltView{bucket})
		if b.trace {
			evaluated = time.Now().UnixNano()
		}
		if e != nil {
			businessErr = e
			return e
		}
		changed := false
		for _, c := range cs {
			old := bucket.Get(c.Key)
			if c.Delete && old == nil {
				continue
			}
			if !c.Delete && old != nil && bytes.Equal(old, c.Value) {
				continue
			}
			changed = true
			if c.Delete {
				e = bucket.Delete(c.Key)
			} else {
				e = bucket.Put(c.Key, c.Value)
			}
			if e != nil {
				return e
			}
		}
		if b.trace {
			writes = time.Now().UnixNano()
		}
		if !changed {
			return errNoWrites
		}
		return nil
	})
	if b.trace {
		returned := time.Now().UnixNano()
		after := b.db.Stats()
		diff := after.Sub(&before)
		requesttrace.Consensus.Mark("store_update", "requested_ns", requested, "locked_ns", locked,
			"callback_ns", callback, "evaluated_ns", evaluated, "writes_ns", writes, "returned_ns", returned,
			"write_ns", int64(diff.TxStats.GetWriteTime()), "spill_ns", int64(diff.TxStats.GetSpillTime()),
			"rebalance_ns", int64(diff.TxStats.GetRebalanceTime()), "write_count", diff.TxStats.GetWrite(),
			"page_bytes", diff.TxStats.GetPageAlloc(), "no_changes", errors.Is(err, errNoWrites), "failed", err != nil && !errors.Is(err, errNoWrites))
	}
	if errors.Is(err, errNoWrites) {
		return nil
	}
	if err != nil && businessErr == nil {
		b.uncertain = true
		return errors.Join(ErrUncertain, err)
	}
	return err
}
func (b *Bolt) Close() error { b.writeMu.Lock(); defer b.writeMu.Unlock(); return b.db.Close() }
