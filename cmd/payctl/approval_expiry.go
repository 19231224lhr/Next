package main

import (
	"time"
	"utxo/crypto/chameleon"
	"utxo/protocol"
)

// Only fresh wallet requests acquire D_T. Saved bytes are reused on retries.
func newWalletFastTx(body protocol.TxBody, claims []protocol.InputClaim, key *chameleon.Public) (protocol.FastTx, error) {
	return protocol.NewExpiringFastTx(body, claims, key, time.Now().Unix())
}
