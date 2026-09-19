package transport

import (
	"context"
	"fmt"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	core "github.com/cometbft/cometbft/rpc/core/types"
	"utxo/finality"
)

func (c *CommitteeClient) Block(ctx context.Context, height int64) (p finality.BlockData, err error) {
	get := func(path string, v any) error {
		raw, e := c.get(ctx, path, 64<<20)
		if e != nil {
			return e
		}
		return cmtjson.Unmarshal(raw, v)
	}
	var next core.ResultCommit
	// Ask for the next header first: no repeated large block fetch while idle.
	if err = get(fmt.Sprintf("/commit?height=%d", height+1), &next); err != nil {
		return
	}
	var b core.ResultBlock
	if err = get(fmt.Sprintf("/block?height=%d", height), &b); err != nil {
		return
	}
	var results core.ResultBlockResults
	if err = get(fmt.Sprintf("/block_results?height=%d", height), &results); err != nil {
		return
	}
	if results.Height != height {
		err = finality.ErrProof
		return
	}
	p = finality.BlockData{Block: b.Block, Results: results.TxsResults, Next: next.SignedHeader}
	return
}
