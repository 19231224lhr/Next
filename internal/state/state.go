package state

import (
	"bytes"
	"errors"
	"sort"
)

var ErrNotFound = errors.New("state not found")

type ReadView interface{ Get([]byte) ([]byte, error) }
type Change struct {
	Key, Value []byte
	Delete     bool
}

// Overlay isolates one business operation until its complete change set is accepted.
type Overlay struct {
	base    ReadView
	changes map[string]Change
}

func NewOverlay(base ReadView) *Overlay {
	return &Overlay{base: base, changes: make(map[string]Change)}
}
func (o *Overlay) Get(k []byte) ([]byte, error) {
	if c, ok := o.changes[string(k)]; ok {
		if c.Delete {
			return nil, ErrNotFound
		}
		return bytes.Clone(c.Value), nil
	}
	if o.base == nil {
		return nil, ErrNotFound
	}
	return o.base.Get(k)
}
func (o *Overlay) Set(k, v []byte) {
	o.changes[string(k)] = Change{Key: bytes.Clone(k), Value: bytes.Clone(v)}
}
func (o *Overlay) Delete(k []byte) { o.changes[string(k)] = Change{Key: bytes.Clone(k), Delete: true} }
func (o *Overlay) Apply(cs []Change) {
	for _, c := range cs {
		if c.Delete {
			o.Delete(c.Key)
		} else {
			o.Set(c.Key, c.Value)
		}
	}
}
func (o *Overlay) Changes() []Change {
	out := make([]Change, 0, len(o.changes))
	for _, c := range o.changes {
		c.Key = bytes.Clone(c.Key)
		c.Value = bytes.Clone(c.Value)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].Key, out[j].Key) < 0 })
	return out
}
