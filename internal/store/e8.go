package store

import (
	"os"
	"sync"
	"utxo/internal/state"
)

var e8Stores struct {
	sync.Mutex
	DBs []*Ephemeral
}

func registerE8Store(db *Ephemeral) {
	if os.Getenv("UTXO_E8_METRICS") != "1" {
		return
	}
	e8Stores.Lock()
	defer e8Stores.Unlock()
	e8Stores.DBs = append(e8Stores.DBs, db)
}

// Endpoint callers request this only outside the sending window. It scans once
// without allocating a second copy of values; byte counts are logical KV bytes.
func E8StateSizes() map[string]map[uint8][2]uint64 {
	e8Stores.Lock()
	defer e8Stores.Unlock()
	out := map[string]map[uint8][2]uint64{}
	for _, db := range e8Stores.DBs {
		db.mu.RLock()
		rows := map[uint8][2]uint64{}
		if !db.closed {
			db.view.tree.Ascend(func(e state.Entry) bool {
				x := rows[e.Key[0]]
				x[0]++
				x[1] += uint64(len(e.Key) + len(e.Value))
				rows[e.Key[0]] = x
				return true
			})
		}
		out[db.identity.Role+"/"+db.identity.Node] = rows
		db.mu.RUnlock()
	}
	return out
}
