package state

import (
	"bytes"
	"errors"
	"sort"
)

// ScanView is used by bounded maintenance queues, never to infer absence of a
// spend from an event stream. Results are exclusive of After and copied.
type Entry struct{ Key, Value []byte }
type ScanView interface {
	Scan(prefix, after []byte, limit int) ([]Entry, error)
}

// Scan merges pending writes with the underlying queue snapshot. Deletions can
// hide at most len(changes) base entries, so one bounded over-read suffices.
func (o *Overlay) Scan(prefix, after []byte, limit int) ([]Entry, error) {
	if limit <= 0 {
		return nil, nil
	}
	scanner, ok := o.base.(ScanView)
	if !ok {
		return nil, errors.New("overlay base does not support scan")
	}
	if limit > int(^uint(0)>>1)-len(o.changes) {
		return nil, errors.New("scan limit overflow")
	}
	base, e := scanner.Scan(prefix, after, limit+len(o.changes))
	if e != nil {
		return nil, e
	}
	rows := make(map[string]Entry, len(base)+len(o.changes))
	for _, r := range base {
		rows[string(r.Key)] = r
	}
	for _, c := range o.changes {
		if !bytes.HasPrefix(c.Key, prefix) || bytes.Compare(c.Key, after) <= 0 {
			continue
		}
		if c.Delete {
			delete(rows, string(c.Key))
		} else {
			rows[string(c.Key)] = Entry{bytes.Clone(c.Key), bytes.Clone(c.Value)}
		}
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].Key, out[j].Key) < 0 })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
