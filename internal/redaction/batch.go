//go:build comet_v3

package redaction

import (
	"bytes"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmtstore "github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
	"sort"
	"utxo/crypto/chameleon"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

type BatchTask struct {
	Command protocol.RepairBatch
	Body    []byte
	Height  int64
}
type batchReference struct {
	Batch protocol.Hash
	Index uint32
}

func BatchTaskKey(id protocol.Hash) []byte      { return state.Key(114, id[:]) }
func batchReferenceKey(id protocol.Hash) []byte { return state.Key(115, id[:]) }

// LoadTask projects a batch item for legacy observation APIs without storing
// the full batch body once per obligation. Batch commands stay immutable.
func LoadTask(v state.ReadView, id protocol.Hash) (Task, bool, error) {
	task, found, err := state.Load[Task](v, TaskKey(id))
	if err != nil || found {
		return task, found, err
	}
	ref, hasRef, err := state.Load[batchReference](v, batchReferenceKey(id))
	if err != nil {
		return task, false, err
	}
	batchID := id
	if hasRef {
		batchID = ref.Batch
	}
	batch, found, err := state.Load[BatchTask](v, BatchTaskKey(batchID))
	if err != nil || !found {
		return task, found, err
	}
	index := int(ref.Index)
	if index >= len(batch.Command.Items) {
		return task, false, protocol.ErrRule
	}
	c := batch.Command
	x := c.Items[index]
	block, err := decodeBody(batch.Body)
	if err != nil {
		return task, false, err
	}
	if int(x.Transaction) >= len(block.Data.Txs) {
		return task, false, protocol.ErrRule
	}
	task = Task{Height: batch.Height, Body: batch.Body, Command: protocol.RepairInput{Network: c.Network, Output: x.Output, Height: c.Height, Transaction: x.Transaction, Input: x.Input, Base: c.Base, Previous: c.Previous, Next: c.Next, Parts: c.Parts, TransactionBytes: bytes.Clone(block.Data.Txs[x.Transaction])}}
	return task, true, nil
}

func BuildBatch(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, singles []protocol.RepairInput, now int64) (protocol.RepairBatch, error) {
	var c protocol.RepairBatch
	if len(singles) == 0 || len(singles) > protocol.MaxRepairItems {
		return c, protocol.ErrRule
	}
	for i, s := range singles {
		expected, _, err := InputTarget(v, bs, p, s.Output, now)
		if err != nil {
			return c, err
		}
		if s.Network != expected.Network || s.Height != expected.Height || s.Transaction != expected.Transaction || s.Input != expected.Input {
			return c, protocol.ErrAuth
		}
		pay, err := protocol.DecodeDirectSubmission(s.TransactionBytes)
		if err != nil {
			return c, err
		}
		if int(s.Input) >= len(pay.Tx.Funding) {
			return c, protocol.ErrRule
		}
		if i == 0 {
			c.Network, c.Height, c.Base, c.Previous = expected.Network, expected.Height, expected.Base, expected.Previous
			c.Parts = []chameleon.Opening{{}}
		}
		if expected.Height != c.Height || expected.Base != c.Base || expected.Previous != c.Previous {
			return c, protocol.ErrRule
		}
		c.Items = append(c.Items, protocol.RepairItem{Output: s.Output, Transaction: s.Transaction, Input: s.Input, Opening: pay.Tx.Funding[s.Input].Opening})
	}
	sort.Slice(c.Items, func(i, j int) bool {
		a, b := c.Items[i], c.Items[j]
		return a.Transaction < b.Transaction || (a.Transaction == b.Transaction && a.Input < b.Input)
	})
	outputs := make([]protocol.OutputID, len(c.Items))
	for i, item := range c.Items {
		outputs[i] = item.Output
	}
	if err := preflightRepairs(v, p, outputs, now); err != nil {
		return c, err
	}
	_, body, err := batchBody(v, bs, p, c, now)
	if err != nil {
		return c, err
	}
	c.Next = digest(body)
	return c, nil
}

func batchBody(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, c protocol.RepairBatch, now int64) (*types.Block, []byte, error) {
	if _, err := c.MarshalBinary(); err != nil {
		return nil, nil, err
	}
	old, revision, err := Canonical(v, bs, c.Height)
	if err != nil {
		return nil, nil, err
	}
	if revision.Number != c.Base || digest(revision.Body) != c.Previous {
		return nil, nil, rules.ErrConflict
	}
	pb, err := old.ToProto()
	if err != nil {
		return nil, nil, err
	}
	payments := make(map[uint32]protocol.DirectSubmission)
	for _, x := range c.Items {
		expected, pay, err := InputTarget(v, bs, p, x.Output, now)
		if err != nil {
			return nil, nil, err
		}
		if expected.Network != c.Network || expected.Height != c.Height || expected.Transaction != x.Transaction || expected.Input != x.Input || expected.Base != c.Base || expected.Previous != c.Previous {
			return nil, nil, protocol.ErrAuth
		}
		if previous, ok := payments[x.Transaction]; ok {
			pay = previous
		}
		f := pay.Tx.Funding[x.Input]
		f.Kind = protocol.ReserveFunding
		f.Ref = protocol.ReserveDebitIdentity(c.Network, x.Output)
		f.Opening = x.Opening
		if !p.Key.Verify(pay.Tx.FundingContext(int(x.Input), p.Key.KeyID()), f.ReferenceBytes(), pay.Tx.Commitments[x.Input], f.Opening) {
			return nil, nil, protocol.ErrAuth
		}
		pay.Tx.Funding[x.Input] = f
		payments[x.Transaction] = pay
	}
	for index, pay := range payments {
		raw, err := pay.MarshalBinary()
		if err != nil {
			return nil, nil, err
		}
		if len(raw) != len(pb.Data.Txs[index]) {
			return nil, nil, protocol.ErrRule
		}
		pb.Data.Txs[index] = raw
	}
	b, err := types.BlockFromProto(pb)
	if err != nil {
		return nil, nil, err
	}
	body, err := pb.Marshal()
	return b, body, err
}

func BatchPartRequests(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, c protocol.RepairBatch, now int64) ([]PartRequest, []chameleon.Opening, []byte, error) {
	_, body, err := batchBody(v, bs, p, c, now)
	if err != nil {
		return nil, nil, nil, err
	}
	return partRequestsForBody(v, bs, p, c.Height, body)
}
func BatchPartShares(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, signer chameleon.Signer, c protocol.RepairBatch, now int64) ([]chameleon.Contribution, error) {
	if _, err := c.MarshalBinary(); err != nil {
		return nil, err
	}
	outputs := make([]protocol.OutputID, len(c.Items))
	for i, item := range c.Items {
		outputs[i] = item.Output
	}
	if err := preflightRepairs(v, p, outputs, now); err != nil {
		return nil, err
	}
	requests, _, _, err := BatchPartRequests(v, bs, p, c, now)
	if err != nil {
		return nil, err
	}
	shares := make([]chameleon.Contribution, len(requests))
	for i, r := range requests {
		shares[i], err = signer.Adapt(r.Context, r.Old, r.Next, r.Commitment, r.Opening)
		if err != nil {
			return nil, err
		}
	}
	return shares, nil
}
func CompleteBatchParts(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, c protocol.RepairBatch, now int64, shares [][]chameleon.Contribution) (protocol.RepairBatch, error) {
	requests, openings, body, err := BatchPartRequests(v, bs, p, c, now)
	if err != nil {
		return c, err
	}
	openings, err = combinePartOpenings(p, requests, openings, shares)
	if err != nil {
		return c, err
	}
	c.Parts, c.Next = openings, digest(body)
	return c, nil
}

func ExecuteBatch(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, c protocol.RepairBatch, height, now int64) (state.Transition, error) {
	id := c.ID()
	if id == (protocol.Hash{}) {
		return state.Transition{}, protocol.ErrEncoding
	}
	if _, found, err := state.Load[BatchTask](v, BatchTaskKey(id)); err != nil {
		return state.Transition{}, err
	} else if found {
		raw, e := (protocol.RepairBatchResult{Batch: id}).MarshalBinary()
		return state.Transition{Data: raw}, e
	}
	if c.Height >= height {
		return state.Transition{}, protocol.ErrRule
	}
	b, body, err := batchBody(v, bs, p, c, now)
	if err != nil {
		return state.Transition{}, err
	}
	if digest(body) != c.Next {
		return state.Transition{}, protocol.ErrAuth
	}
	parts, err := types.NewRedactablePartSet(body, types.BlockPartSizeBytes, c.Height, c.Base+1, c.Parts)
	if err != nil {
		return state.Transition{}, err
	}
	meta := bs.LoadBlockMeta(c.Height)
	if meta == nil || !bytes.Equal(b.Hash(), meta.BlockID.Hash) || !parts.Header().Equals(meta.BlockID.PartSetHeader) {
		return state.Transition{}, protocol.ErrAuth
	}
	o := state.NewOverlay(v)
	result := protocol.RepairBatchResult{Batch: id, Applied: true}
	for i, x := range c.Items {
		ob, found, err := state.Load[rules.DirectObligation](o, rules.DirectObligationKey(x.Output))
		if err != nil {
			return state.Transition{}, err
		}
		if !found || ob.Status != rules.DirectOpen {
			return state.Transition{}, rules.ErrConflict
		}
		tr, err := rules.EvaluateDirectCompensation(o, x.Output, p, now)
		if err != nil {
			return state.Transition{}, err
		}
		one, err := protocol.DecodeExecution(tr.Data)
		if err != nil {
			return state.Transition{}, err
		}
		if !one.Applied {
			return state.Transition{}, rules.ErrConflict
		}
		o.Apply(tr.Changes)
		result.Effects = append(result.Effects, protocol.RepairEffect{Output: x.Output, ParentFact: ob.Certificate, ConsumerFact: ob.Consumer, ConsumerTx: ob.Transaction, Input: ob.Input, Amount: ob.Amount, Debit: protocol.ReserveDebitIdentity(c.Network, x.Output)})
		result.FeeOutputs = append(result.FeeOutputs, one.FeeOutputs...)
		if err = state.Put(o, batchReferenceKey(protocol.RepairIdentity(c.Network, x.Output)), batchReference{Batch: id, Index: uint32(i)}); err != nil {
			return state.Transition{}, err
		}
	}
	sort.Slice(result.FeeOutputs, func(i, j int) bool {
		a, b := result.FeeOutputs[i], result.FeeOutputs[j]
		n := bytes.Compare(a.Transaction[:], b.Transaction[:])
		return n < 0 || (n == 0 && a.Index < b.Index)
	})
	raw, err := result.MarshalBinary()
	if err != nil {
		return state.Transition{}, err
	}
	if err = state.Put(o, RevisionKey(c.Height), Revision{Number: c.Base + 1, Body: body}); err != nil {
		return state.Transition{}, err
	}
	if err = state.Put(o, state.Key(110, RevisionKey(c.Height)), c); err != nil {
		return state.Transition{}, err
	}
	if err = state.Put(o, BatchTaskKey(id), BatchTask{Command: c, Body: body, Height: height}); err != nil {
		return state.Transition{}, err
	}
	if err = state.Put(o, QueueKey(height, c.Height, c.Base+1, id), id); err != nil {
		return state.Transition{}, err
	}
	return state.Transition{Changes: o.Changes(), Data: raw}, nil
}

func decodeBody(body []byte) (*types.Block, error) {
	pb := new(cmtproto.Block)
	if err := pb.Unmarshal(body); err != nil {
		return nil, err
	}
	return types.BlockFromProto(pb)
}
