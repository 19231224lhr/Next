//go:build comet_v3

// Package redaction implements the exceptional compensation path. Normal
// payment validation never requests threshold adaptation shares.
package redaction

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmtstore "github.com/cometbft/cometbft/store"
	"github.com/cometbft/cometbft/types"
	"utxo/crypto/chameleon"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

type Revision struct {
	Number uint64
	Body   []byte
}
type Location struct {
	Height int64
	Index  uint32
}

func LocationKey(tx protocol.TxID) []byte { return state.Key(107, tx[:]) }
func RevisionKey(height int64) []byte {
	e := new(protocol.Encoder)
	e.U64(uint64(height))
	return state.Key(108, e.Data())
}
func TaskKey(id protocol.Hash) []byte { return state.Key(109, id[:]) }
func QueueKey(height, target int64, revision uint64, id protocol.Hash) []byte {
	e := new(protocol.Encoder)
	e.U64(uint64(height))
	e.U64(uint64(target))
	e.U64(revision)
	e.Fixed(id[:])
	return state.Key(113, e.Data())
}

type Task struct {
	Command protocol.RepairInput
	Body    []byte
	Height  int64
}

// Canonical reads application-committed revisions, not the background worker's
// materialized view. Worker timing can never change consensus execution.
func Canonical(v state.ReadView, bs *cmtstore.BlockStore, height int64) (*types.Block, Revision, error) {
	revision, found, err := state.Load[Revision](v, RevisionKey(height))
	if err != nil {
		return nil, revision, err
	}
	if found {
		pb := new(cmtproto.Block)
		if err = pb.Unmarshal(revision.Body); err != nil {
			return nil, revision, err
		}
		b, err := types.BlockFromProto(pb)
		return b, revision, err
	}
	b, err := bs.LoadOriginalBlock(height)
	if err != nil {
		return nil, revision, err
	}
	if b == nil {
		return nil, revision, rules.ErrMissing
	}
	pb, err := b.ToProto()
	if err != nil {
		return nil, revision, err
	}
	revision.Body, err = pb.Marshal()
	return b, revision, err
}

func digest(b []byte) protocol.Hash { return protocol.Hash(sha256.Sum256(b)) }

// InputTarget validates responsibility and timing before a member computes its
// local share. No caller supplies an arbitrary RSA representative.
func InputTarget(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, output protocol.OutputID, now int64) (protocol.RepairInput, protocol.DirectPayment, error) {
	var command protocol.RepairInput
	ob, found, err := state.Load[rules.DirectObligation](v, rules.DirectObligationKey(output))
	if err != nil {
		return command, protocol.DirectPayment{}, err
	}
	if !found || ob.Status != rules.DirectOpen {
		return command, protocol.DirectPayment{}, rules.ErrMissing
	}
	if ob.Deadline == 0 || now < ob.Deadline {
		return command, protocol.DirectPayment{}, rules.ErrLimited
	}
	cfg, ok := p.Organizations[ob.Config]
	if !ok || cfg.Org != ob.Issuer {
		return command, protocol.DirectPayment{}, protocol.ErrAuth
	}
	location, found, err := state.Load[Location](v, LocationKey(ob.Transaction))
	if err != nil {
		return command, protocol.DirectPayment{}, err
	}
	if !found {
		return command, protocol.DirectPayment{}, rules.ErrMissing
	}
	b, revision, err := Canonical(v, bs, location.Height)
	if err != nil {
		return command, protocol.DirectPayment{}, err
	}
	if int(location.Index) >= len(b.Data.Txs) {
		return command, protocol.DirectPayment{}, protocol.ErrRule
	}
	payment, err := protocol.DecodeDirectPayment(b.Data.Txs[location.Index])
	if err != nil {
		return command, payment, err
	}
	if payment.Tx.ID() != ob.Transaction || payment.Certificate.QC.Fact != ob.Consumer || int(ob.Input) >= len(payment.Tx.Funding) || payment.Tx.Body.Inputs[ob.Input].Output != output || payment.Tx.Claims[ob.Input].Output.Amount != ob.Amount {
		return command, payment, protocol.ErrAuth
	}
	f := payment.Tx.Funding[ob.Input]
	if f.Kind != protocol.OriginalFunding || f.Ref != protocol.Hash(output) || !p.Key.Verify(payment.Tx.FundingContext(int(ob.Input), p.Key.KeyID()), f.ReferenceBytes(), payment.Tx.Commitments[ob.Input], f.Opening) {
		return command, payment, protocol.ErrAuth
	}
	command = protocol.RepairInput{Network: cfg.Network, Output: output, Height: location.Height, Transaction: location.Index, Input: ob.Input, Base: revision.Number, Previous: digest(revision.Body)}
	return command, payment, nil
}

func InputShare(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, signer chameleon.Signer, output protocol.OutputID, now int64) (chameleon.Contribution, error) {
	command, payment, err := InputTarget(v, bs, p, output, now)
	if err != nil {
		return chameleon.Contribution{}, err
	}
	i := int(command.Input)
	old := payment.Tx.Funding[i]
	next := protocol.Funding{Kind: protocol.ReserveFunding, Ref: protocol.ReserveDebitIdentity(command.Network, output)}
	return signer.Adapt(payment.Tx.FundingContext(i, p.Key.KeyID()), old.ReferenceBytes(), next.ReferenceBytes(), payment.Tx.Commitments[i], old.Opening)
}

// ReplaceInput changes only one fixed-width funding slot; the immutable claim,
// wallet signature, QC, fees and output IDs remain byte-for-byte unchanged.
func ReplaceInput(p rules.DirectPolicy, c protocol.RepairInput, payment protocol.DirectPayment, shares []chameleon.Contribution) (protocol.RepairInput, error) {
	i := int(c.Input)
	if i >= len(payment.Tx.Funding) {
		return c, protocol.ErrRule
	}
	old := payment.Tx.Funding[i]
	next := protocol.Funding{Kind: protocol.ReserveFunding, Ref: protocol.ReserveDebitIdentity(c.Network, c.Output)}
	r, err := p.Key.Combine(payment.Tx.FundingContext(i, p.Key.KeyID()), old.ReferenceBytes(), next.ReferenceBytes(), payment.Tx.Commitments[i], old.Opening, shares)
	if err != nil {
		return c, err
	}
	payment.Tx.Funding = append([]protocol.Funding(nil), payment.Tx.Funding...)
	next.Opening = r
	payment.Tx.Funding[i] = next
	c.TransactionBytes, err = payment.MarshalBinary()
	return c, err
}

func nextBody(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, c protocol.RepairInput, now int64) (*types.Block, []byte, error) {
	expected, payment, err := InputTarget(v, bs, p, c.Output, now)
	if err != nil {
		return nil, nil, err
	}
	if c.Network != expected.Network || c.Height != expected.Height || c.Transaction != expected.Transaction || c.Input != expected.Input || c.Base != expected.Base || c.Previous != expected.Previous {
		return nil, nil, protocol.ErrAuth
	}
	next, err := protocol.DecodeDirectPayment(c.TransactionBytes)
	if err != nil {
		return nil, nil, err
	}
	i := int(c.Input)
	if i >= len(next.Tx.Funding) {
		return nil, nil, protocol.ErrAuth
	}
	f := next.Tx.Funding[i]
	if f.Kind != protocol.ReserveFunding || f.Ref != protocol.ReserveDebitIdentity(c.Network, c.Output) || !p.Key.Verify(payment.Tx.FundingContext(i, p.Key.KeyID()), f.ReferenceBytes(), payment.Tx.Commitments[i], f.Opening) {
		return nil, nil, protocol.ErrAuth
	}
	payment.Tx.Funding[i] = f
	exact, err := payment.MarshalBinary()
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(exact, c.TransactionBytes) {
		return nil, nil, protocol.ErrAuth
	}
	old, _, err := Canonical(v, bs, c.Height)
	if err != nil {
		return nil, nil, err
	}
	if len(c.TransactionBytes) != len(old.Data.Txs[c.Transaction]) {
		return nil, nil, protocol.ErrRule
	}
	pb, err := old.ToProto()
	if err != nil {
		return nil, nil, err
	}
	pb.Data.Txs[c.Transaction] = c.TransactionBytes
	b, err := types.BlockFromProto(pb)
	if err != nil {
		return nil, nil, err
	}
	body, err := pb.Marshal()
	return b, body, err
}

type PartRequest struct {
	Index              uint32
	Context, Old, Next []byte
	Commitment         chameleon.Commitment
	Opening            chameleon.Opening
}

// PartRequests validates the proposed input repair first. Each signer runs
// this function against its own committed state before providing part shares.
func PartRequests(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, c protocol.RepairInput, now int64) ([]PartRequest, []chameleon.Opening, []byte, error) {
	_, next, err := nextBody(v, bs, p, c, now)
	if err != nil {
		return nil, nil, nil, err
	}
	_, revision, err := Canonical(v, bs, c.Height)
	if err != nil {
		return nil, nil, nil, err
	}
	var oldOpenings []chameleon.Opening
	if revision.Number > 0 {
		// The preceding command is stored with its materialization; never infer
		// current authorization from the mutable local block store.
		old, found, err := state.Load[protocol.RepairInput](v, state.Key(110, RevisionKey(c.Height)))
		if err != nil || !found {
			return nil, nil, nil, rules.ErrMissing
		}
		oldOpenings = old.Parts
	}
	oldParts, err := types.NewRedactablePartSet(revision.Body, types.BlockPartSizeBytes, c.Height, revision.Number, oldOpenings)
	if err != nil {
		return nil, nil, nil, err
	}
	openings := make([]chameleon.Opening, oldParts.Total())
	var requests []PartRequest
	for i := range openings {
		part := oldParts.GetPart(i)
		start := i * int(types.BlockPartSizeBytes)
		end := min(start+int(types.BlockPartSizeBytes), len(next))
		if start >= len(next) {
			return nil, nil, nil, protocol.ErrRule
		}
		openings[i] = part.Redaction.Opening
		if bytes.Equal(part.Bytes, next[start:end]) {
			continue
		}
		ctx := types.RedactionContext(c.Height, uint32(i))
		commitment, err := p.Key.Digest(ctx, part.Bytes, openings[i])
		if err != nil {
			return nil, nil, nil, err
		}
		requests = append(requests, PartRequest{Index: uint32(i), Context: ctx, Old: part.Bytes, Next: next[start:end], Commitment: commitment, Opening: openings[i]})
	}
	return requests, openings, next, nil
}

func PartShares(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, signer chameleon.Signer, c protocol.RepairInput, now int64) ([]chameleon.Contribution, error) {
	requests, _, _, err := PartRequests(v, bs, p, c, now)
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

func CompleteParts(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, c protocol.RepairInput, now int64, shares [][]chameleon.Contribution) (protocol.RepairInput, error) {
	requests, openings, body, err := PartRequests(v, bs, p, c, now)
	if err != nil {
		return c, err
	}
	if len(shares) < 3 || len(shares) > 4 {
		return c, protocol.ErrAuth
	}
	for i, r := range requests {
		var votes []chameleon.Contribution
		for _, member := range shares {
			if len(member) != len(requests) {
				return c, protocol.ErrAuth
			}
			votes = append(votes, member[i])
		}
		openings[r.Index], err = p.Key.Combine(r.Context, r.Old, r.Next, r.Commitment, r.Opening, votes)
		if err != nil {
			return c, err
		}
	}
	c.Parts = openings
	c.Next = digest(body)
	return c, nil
}

func Execute(v state.ReadView, bs *cmtstore.BlockStore, p rules.DirectPolicy, c protocol.RepairInput, height, now int64) (state.Transition, error) {
	id := protocol.RepairIdentity(c.Network, c.Output)
	if old, found, err := state.Load[Task](v, TaskKey(id)); err != nil {
		return state.Transition{}, err
	} else if found {
		a, _ := old.Command.MarshalBinary()
		b, _ := c.MarshalBinary()
		if bytes.Equal(a, b) {
			return state.Transition{}, nil
		}
		return state.Transition{}, rules.ErrConflict
	}
	if c.Height >= height {
		return state.Transition{}, protocol.ErrRule
	}
	b, body, err := nextBody(v, bs, p, c, now)
	if err != nil {
		return state.Transition{}, err
	}
	if c.Next != digest(body) {
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
	tr, err := rules.EvaluateDirectCompensation(v, c.Output, p, now)
	if err != nil {
		return state.Transition{}, err
	}
	o := state.NewOverlay(v)
	o.Apply(tr.Changes)
	if err = state.Put(o, RevisionKey(c.Height), Revision{Number: c.Base + 1, Body: body}); err != nil {
		return state.Transition{}, err
	}
	if err = state.Put(o, state.Key(110, RevisionKey(c.Height)), c); err != nil {
		return state.Transition{}, err
	}
	if err = state.Put(o, TaskKey(id), Task{Command: c, Body: body, Height: height}); err != nil {
		return state.Transition{}, err
	}
	if err = state.Put(o, QueueKey(height, c.Height, c.Base+1, id), id); err != nil {
		return state.Transition{}, err
	}
	tr.Changes = o.Changes()
	return tr, nil
}

// Materialize is idempotent after Commit. It verifies the application-committed
// exact bytes; an isolated hash collision never grants editing authority.
func Materialize(v state.ReadView, bs *cmtstore.BlockStore, id protocol.Hash) error {
	task, found, err := state.Load[Task](v, TaskKey(id))
	if err != nil {
		return err
	}
	if !found {
		return rules.ErrMissing
	}
	c := task.Command
	// The local cursor may be lost while the block store retains newer repairs.
	// Only ReviseBlock writes this revision metadata, after exact authorization.
	// A later authorized revision already incorporates this task; never undo it.
	part := bs.LoadBlockPart(c.Height, 0)
	if part != nil && part.Redaction != nil && part.Redaction.Revision > c.Base+1 {
		_, committed, err := Canonical(v, bs, c.Height)
		if err != nil {
			return err
		}
		if part.Redaction.Revision > committed.Number {
			return rules.ErrConflict
		}
		return nil
	}
	parts, err := types.NewRedactablePartSet(task.Body, types.BlockPartSizeBytes, c.Height, c.Base+1, c.Parts)
	if err != nil {
		return err
	}
	return bs.ReviseBlock(c.Height, c.Base, parts, func(_, next *types.Block) error {
		pb, err := next.ToProto()
		if err != nil {
			return err
		}
		body, err := pb.Marshal()
		if err != nil {
			return err
		}
		if !bytes.Equal(body, task.Body) || digest(body) != c.Next {
			return fmt.Errorf("repair body differs from committed command")
		}
		return nil
	})
}
