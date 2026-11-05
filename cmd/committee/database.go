package main

import (
	"os"

	dbm "github.com/cometbft/cometbft-db"
	cmtcfg "github.com/cometbft/cometbft/config"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

func directDBProvider(ctx *cmtcfg.DBContext) (dbm.DB, error) {
	// Experimental mode deliberately drops Comet's databases on process exit.
	// Keep each store independent: Comet uses state and evidence alongside the
	// blockstore, and sharing one MemDB would change their key spaces and
	// lifecycle semantics.
	if os.Getenv("UTXO_EXPERIMENT_MEM_BLOCKSTORE") == "1" &&
		(ctx.ID == "blockstore" || ctx.ID == "state" || ctx.ID == "evidence") {
		return dbm.NewMemDB(), nil
	}
	if ctx.ID != "blockstore" || ctx.Config.DBBackend != string(dbm.GoLevelDBBackend) {
		return cmtcfg.DefaultDBProvider(ctx)
	}
	// Larger memtables avoid repeatedly compacting small, overlapping block
	// tables. This does not change each block's original WriteSync requirement.
	return dbm.NewGoLevelDBWithOpts(ctx.ID, ctx.Config.DBDir(), &opt.Options{WriteBuffer: 64 << 20})
}
