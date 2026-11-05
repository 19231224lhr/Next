//go:build !comet_v3

package main

import (
	"context"
	"fmt"
	"github.com/cometbft/cometbft/node"
	"net/http"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/committee"
	"utxo/internal/store"
)

func configureDirect(n cfg.Network, e *committee.Engine) error {
	if n.Direct != nil {
		return fmt.Errorf("v3 requires the pinned Comet fork and -tags=comet_v3")
	}
	return nil
}
func startRepairRuntime(context.Context, configuration, cfg.Network, store.Store, *committee.App, *node.Node, *http.ServeMux) (func(), error) {
	return func() {}, nil
}
