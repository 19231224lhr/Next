# 本轮提供给 GPT 的实际代码

版本：`6aa0d71`，CometBFT v0.38.26 固定分支。这里只收录当前实现，不代表建议修改。

## internal.transport.Block

```go
func (c *CommitteeClient) Block(ctx context.Context, height int64) (p finality.BlockData, err error) {
	get := func(path string, v any) error {
		started := time.Now().UnixNano()
		raw, e := c.get(ctx, path, 64<<20)
		if e != nil {
			return e
		}
		err := cmtjson.Unmarshal(raw, v)
		if err == nil {
			requesttrace.Consensus.Mark("follow_fetch", "height", height, "path", path, "start_ns", started, "bytes", len(raw))
		}
		return err
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
```

## finality.VerifyBlock

```go
func VerifyBlock(t Trust, p BlockData) (VerifiedBlock, error) {
	fail := VerifiedBlock{}
	b, h := p.Block, p.Next
	if t.Validate() != nil || b == nil || h.Header == nil || h.Commit == nil || b.ChainID != t.ChainID || h.Height != b.Height+1 || b.Height < 1 || len(b.Txs) != len(p.Results) {
		return fail, ErrProof
	}
	if err := b.ValidateBasic(); err != nil {
		return fail, fmt.Errorf("block %d: %w", b.Height, err)
	}
	if err := h.ValidateBasic(t.ChainID); err != nil {
		return fail, fmt.Errorf("next header: %w", err)
	}
	if !bytes.Equal(h.ValidatorsHash, t.Validators.Hash()) || !bytes.Equal(h.NextValidatorsHash, t.Validators.Hash()) || !bytes.Equal(h.LastBlockID.Hash, b.Hash()) {
		return fail, fmt.Errorf("block/header linkage: %w", ErrProof)
	}
	// Compute from supplied bytes, not cached fields in the decoded block.
	data := ct.Data{Txs: b.Txs}
	if !bytes.Equal(data.Hash(), b.DataHash) {
		return fail, fmt.Errorf("transaction root: %w", ErrProof)
	}
	for _, r := range p.Results {
		if r == nil {
			return fail, ErrProof
		}
	}
	if !bytes.Equal(ct.NewResults(p.Results).Hash(), h.LastResultsHash) {
		return fail, fmt.Errorf("execution root at %d: %w", b.Height, ErrProof)
	}
	if err := t.Validators.VerifyCommitLight(t.ChainID, h.Commit.BlockID, h.Height, h.Commit); err != nil {
		return fail, err
	}
	out := VerifiedBlock{height: b.Height, hash: bytes.Clone(b.Hash()), previous: bytes.Clone(b.LastBlockID.Hash)}
	for i, tx := range b.Txs {
		r := p.Results[i]
		out.txs = append(out.txs, ExecutedTx{bytes.Clone(tx), r.Code, bytes.Clone(r.Data)})
	}
	return out, nil
}
```

## internal.committee.Commit

```go
func (a *App) Commit(context.Context, *abci.RequestCommit) (*abci.ResponseCommit, error) {
	var entered time.Time
	var lockWait time.Duration
	if requesttrace.Consensus != nil {
		entered = time.Now()
	}
	a.mu.Lock()
	if !entered.IsZero() {
		lockWait = time.Since(entered)
	}
	defer a.mu.Unlock()
	if a.halted != nil {
		return nil, a.halted
	}
	if a.pending == nil {
		return &abci.ResponseCommit{}, nil
	}
	requesttrace.Settlement.Block(a.pending.Height, "commit_start")
	requesttrace.Consensus.Mark("commit_start", "height", a.pending.Height)
	if !entered.IsZero() {
		requesttrace.Consensus.Mark("commit_lock", "height", a.pending.Height, "wait_ns", lockWait.Nanoseconds())
	}
	raw, e := json.Marshal(a.pending)
	if e != nil {
		return nil, e
	}
	cs := append([]state.Change(nil), a.pendingChanges...)
	cs = append(cs, state.Change{Key: metaKey, Value: raw})
	if a.executeAt == nil {
		cs = append(cs, state.Change{Key: blockKey(a.pending.Height), Value: raw})
	}
	for i, rawFact := range a.pending.Leaves {
		f, e := protocol.DecodeFact(rawFact)
		if e != nil {
			return nil, e
		}
		location, _ := json.Marshal(factLocation{Height: a.pending.Height, Index: i})
		cs = append(cs, state.Change{Key: proofKey(f.ID()), Value: location})
	}
	if requesttrace.Consensus != nil {
		bytes := 0
		for _, c := range cs {
			bytes += len(c.Key) + len(c.Value)
		}
		requesttrace.Consensus.Mark("commit_update_start", "height", a.pending.Height, "changes", len(cs), "bytes", bytes)
	}
	e = a.db.Update(func(state.ReadView) ([]state.Change, error) {
		requesttrace.Consensus.Mark("commit_update_callback", "height", a.pending.Height)
		return cs, nil
	})
	requesttrace.Consensus.Mark("commit_update_return", "height", a.pending.Height)
	if e != nil {
		a.halted = e
		return nil, e
	}
	a.committed = *a.pending
	a.response = new(abci.ResponseFinalizeBlock)
	if e = a.response.Unmarshal(a.pending.Response); e != nil {
		a.halted = e
		return nil, e
	}
	requesttrace.Settlement.Block(a.pending.Height, "commit_done")
	requesttrace.Consensus.Mark("commit_done", "height", a.pending.Height)
	a.pending = nil
	a.pendingChanges = nil
	return &abci.ResponseCommit{}, nil
}
```
