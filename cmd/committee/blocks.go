package main

import (
	cmtjson "github.com/cometbft/cometbft/libs/json"
	"github.com/cometbft/cometbft/rpc/client/local"
	ct "github.com/cometbft/cometbft/types"
	"net/http"
	"strconv"
)

// Bind once before startup. Following executes original claims; later repairs
// are processed at their own committed height, never retrospectively.
var originalBlock func(int64) (*ct.Block, error)

func blockRoutes(mux *http.ServeMux, c *local.Local) {
	for _, path := range []string{"block", "block_results", "commit"} {
		mux.HandleFunc("GET /"+path, func(w http.ResponseWriter, r *http.Request) {
			height, err := strconv.ParseInt(r.URL.Query().Get("height"), 10, 64)
			if err != nil || height < 1 {
				http.Error(w, "invalid height", 400)
				return
			}
			var result any
			switch path {
			case "block":
				b, e := c.Block(r.Context(), &height)
				err = e
				if err == nil && originalBlock != nil {
					b.Block, err = originalBlock(height)
				}
				result = b
			case "block_results":
				result, err = c.BlockResults(r.Context(), &height)
			case "commit":
				result, err = c.Commit(r.Context(), &height)
			}
			if err != nil {
				http.Error(w, "height unavailable", 404)
				return
			}
			raw, err := cmtjson.Marshal(result)
			if err != nil {
				http.Error(w, "encoding error", 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw)
		})
	}
}
