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
	"utxo/internal/state"
)

var dataBucket = []byte("state.v2")
var metaBucket = []byte("identity.v2")

type Bolt struct {
	db        *bolt.DB
	writeMu   sync.Mutex
	uncertain bool
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
	db, e := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
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
	return &Bolt{db: db}, nil
}
func (b *Bolt) View(fn func(state.ReadView) error) error {
	return b.db.View(func(tx *bolt.Tx) error { return fn(boltView{tx.Bucket(dataBucket)}) })
}
func (b *Bolt) Update(fn func(state.ReadView) ([]state.Change, error)) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if b.uncertain {
		return ErrUncertain
	}
	var businessErr error
	err := b.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(dataBucket)
		cs, e := fn(boltView{bucket})
		if e != nil {
			businessErr = e
			return e
		}
		for _, c := range cs {
			if c.Delete {
				e = bucket.Delete(c.Key)
			} else {
				e = bucket.Put(c.Key, c.Value)
			}
			if e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil && businessErr == nil {
		b.uncertain = true
		return errors.Join(ErrUncertain, err)
	}
	return err
}
func (b *Bolt) Close() error { b.writeMu.Lock(); defer b.writeMu.Unlock(); return b.db.Close() }
