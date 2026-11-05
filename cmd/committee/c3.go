package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"utxo/internal/committee"
	"utxo/protocol"
)

func addC3Queries(mux *http.ServeMux, e *committee.Engine) {
	mux.HandleFunc("GET /v3/c3/escrows/{spend}", func(w http.ResponseWriter, r *http.Request) {
		var id protocol.Hash
		if err := id.UnmarshalText([]byte(r.PathValue("spend"))); err != nil {
			http.Error(w, "INVALID_ID", 400)
			return
		}
		s, err := e.C3Fee(protocol.SpendFactID(id))
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s)
	})
	mux.HandleFunc("GET /v3/c3/pools/{account}/{version}", func(w http.ResponseWriter, r *http.Request) {
		var id protocol.Hash
		if err := id.UnmarshalText([]byte(r.PathValue("account"))); err != nil {
			http.Error(w, "INVALID_ID", 400)
			return
		}
		version, err := strconv.ParseUint(r.PathValue("version"), 10, 64)
		if err != nil {
			http.Error(w, "INVALID_VERSION", 400)
			return
		}
		s, err := e.C3Pool(protocol.ResourceKey{Kind: protocol.ResourceFUEL, Account: id, Version: version})
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s)
	})
}
