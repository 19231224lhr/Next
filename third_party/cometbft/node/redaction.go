package node

import "github.com/cometbft/cometbft/store"

// BeforeReplay binds the application's repair validator to this exact store
// before the startup handshake can replay immutable RepairInput commands.
// Set once during process initialization, before calling NewNode.
var BeforeReplay func(*store.BlockStore) error
