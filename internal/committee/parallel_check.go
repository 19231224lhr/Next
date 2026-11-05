package committee

import (
	"context"

	abcicli "github.com/cometbft/cometbft/abci/client"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/proxy"
)

// NewParallelCheckClientCreator warms the existing static-verification cache
// before entering Comet's serial mempool connection. Admission and its callbacks
// still use the original client, including invalid and evicted transactions.
func NewParallelCheckClientCreator(app abci.Application, engine *Engine) proxy.ClientCreator {
	return &parallelCheckCreator{proxy.NewConnSyncLocalClientCreator(app), engine.Check, make(chan struct{}, 4)}
}

type parallelCheckCreator struct {
	proxy.ClientCreator
	check func([]byte) error
	slots chan struct{}
}

func (c *parallelCheckCreator) NewABCIClient() (abcicli.Client, error) {
	client, err := c.ClientCreator.NewABCIClient()
	if err != nil {
		return nil, err
	}
	return &parallelCheckClient{client, c}, nil
}

func (c *parallelCheckCreator) warm(raw []byte) {
	c.slots <- struct{}{}
	defer func() { <-c.slots }()
	// The authoritative CheckTx below supplies the normal rejection response.
	_ = c.check(raw)
}

type parallelCheckClient struct {
	abcicli.Client
	creator *parallelCheckCreator
}

func (c *parallelCheckClient) CheckTxAsync(ctx context.Context, req *abci.RequestCheckTx) (*abcicli.ReqRes, error) {
	if req.Type == abci.CheckTxType_New {
		c.creator.warm(req.Tx)
	}
	return c.Client.CheckTxAsync(ctx, req)
}
