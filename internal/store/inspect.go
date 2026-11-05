package store

import (
	"errors"
	bolt "go.etcd.io/bbolt"
	"time"
	"utxo/internal/state"
)

// Inspect opens a stopped instance read-only for experiment accounting.
// A live writer retains its lock; this function does not bypass it.
func Inspect(path string, fn func(state.ReadView) error) error {
	db, e := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if e != nil {
		return e
	}
	defer db.Close()
	return db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(dataBucket)
		if bucket == nil {
			return errors.New("missing state bucket")
		}
		return fn(boltView{bucket})
	})
}
