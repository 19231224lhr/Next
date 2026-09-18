package store

import (
	"bytes"
	"errors"
	"sort"
	"utxo/internal/state"
)

func Scan(db Store, prefix, after []byte, limit int) (entries []state.Entry, err error) {
	if limit < 1 || limit > 1024 {
		return nil, errors.New("invalid page size")
	}
	err = db.View(func(v state.ReadView) error {
		scanner, ok := v.(state.ScanView)
		if !ok {
			return errors.New("store does not support maintenance scans")
		}
		entries, err = scanner.Scan(prefix, after, limit)
		return err
	})
	return
}
func (v memoryView) Scan(prefix, after []byte, limit int) ([]state.Entry, error) {
	var keys []string
	for key := range v {
		if bytes.HasPrefix([]byte(key), prefix) && bytes.Compare([]byte(key), after) > 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	result := make([]state.Entry, 0, len(keys))
	for _, key := range keys {
		result = append(result, state.Entry{Key: []byte(key), Value: bytes.Clone(v[key])})
	}
	return result, nil
}
func (v boltView) Scan(prefix, after []byte, limit int) ([]state.Entry, error) {
	c := v.b.Cursor()
	start := prefix
	if bytes.Compare(after, start) > 0 {
		start = after
	}
	k, b := c.Seek(start)
	if k != nil && bytes.Equal(k, after) {
		k, b = c.Next()
	}
	result := make([]state.Entry, 0, limit)
	for ; k != nil && bytes.HasPrefix(k, prefix) && len(result) < limit; k, b = c.Next() {
		result = append(result, state.Entry{Key: bytes.Clone(k), Value: bytes.Clone(b)})
	}
	return result, nil
}
