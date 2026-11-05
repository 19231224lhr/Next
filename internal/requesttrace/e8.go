package requesttrace

import (
	"os"
	"sync"
	"time"
)

var e8Enabled = os.Getenv("UTXO_E8_METRICS") == "1"

type E8Block struct{ Height, NS int64 }

var e8Blocks struct {
	sync.Mutex
	Rows []E8Block
}

func E8Commit(h int64) {
	if !e8Enabled {
		return
	}
	e8Blocks.Lock()
	defer e8Blocks.Unlock()
	if len(e8Blocks.Rows) < 65536 {
		e8Blocks.Rows = append(e8Blocks.Rows, E8Block{h, time.Now().UnixNano()})
	}
}
func E8Blocks() []E8Block {
	e8Blocks.Lock()
	defer e8Blocks.Unlock()
	return append([]E8Block(nil), e8Blocks.Rows...)
}
