//go:build !comet_v3

package main

import (
	"fmt"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/state"
)

func auditDirectHistory(state.ReadView, cfg.Network, string) (int, error) {
	return 0, fmt.Errorf("v3 history audit requires -tags=comet_v3")
}
