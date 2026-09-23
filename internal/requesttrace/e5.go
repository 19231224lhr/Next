package requesttrace

// E5 records a few delivery boundaries without enabling the full diagnostic trace.
import (
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"
	"utxo/protocol"
)

type e5FactTiming struct {
	First map[string]int64
	Count map[string]int
}

var e5Events = struct {
	sync.Mutex
	Enabled bool
	Facts   map[string]*e5FactTiming
	Dropped int
}{Enabled: os.Getenv("UTXO_E5_OBSERVE") == "1", Facts: map[string]*e5FactTiming{}}

func e5Payment(stage string, fact protocol.SpendFactID) {
	if !e5Events.Enabled {
		return
	}
	switch stage {
	case "certificate_ready", "persist_background_queued", "submit_start", "submit_done":
	default:
		return
	}
	now := time.Now().UnixNano()
	id := protocol.Hash(fact).String()
	e5Events.Lock()
	defer e5Events.Unlock()
	e := e5Events.Facts[id]
	if e == nil {
		if len(e5Events.Facts) >= 30100 {
			e5Events.Dropped++
			return
		}
		e = &e5FactTiming{First: map[string]int64{}, Count: map[string]int{}}
		e5Events.Facts[id] = e
	}
	if e.First[stage] == 0 {
		e.First[stage] = now
	}
	e.Count[stage]++
}

func registerE5(mux *http.ServeMux) {
	if !e5Events.Enabled {
		return
	}
	mux.HandleFunc("GET /debug/e5", func(w http.ResponseWriter, r *http.Request) {
		e5Events.Lock()
		defer e5Events.Unlock()
		_ = json.NewEncoder(w).Encode(struct {
			Facts   map[string]*e5FactTiming
			Dropped int
		}{e5Events.Facts, e5Events.Dropped})
	})
}
