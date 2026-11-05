package main

import (
	"fmt"
	"os"
	"utxo/internal/store"
)

// The explicit memory mode is for fresh experiments, not node recovery.
// Commit still atomically applies a whole block; Close exports an audit only.
func openCommitteeStore(path string, id store.Identity, direct bool) (store.Store, error) {
	if os.Getenv("UTXO_EXPERIMENT_COMMITTEE_MEMORY") != "1" {
		return store.Open(path, id)
	}
	if !direct || os.Getenv("UTXO_EXPERIMENT_MEM_BLOCKSTORE") != "1" {
		return nil, fmt.Errorf("committee memory experiment requires direct protocol and in-memory Comet storage")
	}
	return store.OpenEphemeral(path, id)
}
