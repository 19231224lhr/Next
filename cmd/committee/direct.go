//go:build comet_v3

package main

import (
	"github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
	cfg "utxo/cmd/internal/config"
	"utxo/internal/committee"
)

func configureDirect(n cfg.Network, e *committee.Engine) error {
	if n.Direct == nil {
		return nil
	}
	p, err := n.Direct.Policy(n.Schedule, n.Organizations)
	if err != nil {
		return err
	}
	if err = types.ConfigureRedaction(n.ChainID, p.Key); err != nil {
		return err
	}
	node.BeforeReplay = func(bs *store.BlockStore) error { originalBlock = bs.LoadOriginalBlock; return e.EnableRepair(bs) }
	return nil
}
