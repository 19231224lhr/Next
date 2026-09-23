package requesttrace

import (
	"encoding/json"
	"net/http"
	"utxo/protocol"
)

// Payment records only local diagnostics; disabled tracing does not encode IDs.
func Payment(stage string, fact protocol.SpendFactID) {
	e5Payment(stage, fact)
	if Consensus != nil {
		Consensus.Mark(stage, "spend", protocol.Hash(fact).String())
	}
}

// RegisterTimeline exposes the bounded trace only in explicitly enabled experiments.
func RegisterTimeline(mux *http.ServeMux) {
	registerE5(mux)
	if Consensus != nil {
		mux.HandleFunc("GET /debug/timeline", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Consensus.Snapshot())
		})
	}
}
