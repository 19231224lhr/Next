//go:build comet_v3

package redaction

import (
	"bytes"
	"fmt"
	"github.com/cometbft/cometbft/types"
	"utxo/crypto/chameleon"
	"utxo/finality"
	"utxo/internal/rules"
	"utxo/protocol"
)

// All receipt verification APIs below prove positive historical authorization
// under caller-pinned chain/network/committee and application semantics. They
// never prove latest/current state, absence of later events or checkpoint
// ancestry. An authorized-by-H caller must separately require that the signed
// successor exposing the execution results is available within its trusted H.
// DirectPolicy and organization/CH-key configuration are locally trusted inputs,
// not authority selected by a proof package. Verification reads no database.

type HistoricalReceipt struct {
	OriginalBody, BaseBody, Body []byte
	OriginalCommit               *types.Commit
	Execution                    finality.BlockData
	CommandIndex                 uint32
}

// LeanHistoricalReceipt relies on the pinned committee's successful execution
// of the repair rules; it does not re-execute the original-to-base diff.
type LeanHistoricalReceipt struct {
	Body           []byte
	OriginalCommit *types.Commit
	Execution      finality.BlockData
	CommandIndex   uint32
}
type AuthorizedFunding struct {
	Output               protocol.OutputID
	Transaction, Input   uint32
	ConsumerTx           protocol.TxID
	Organization         protocol.Hash
	Amount               uint64
	Decision, Debit      protocol.Hash
	Original, Authorized protocol.Funding
	OriginalAvailable    bool
}

// VerifyHistoricalAuthorization proves one exact historical body was applied
// under the pinned rules, then verifies its original full BlockID compatibility.
// OriginalFunding bytes are not supplied or claimed by this lean witness.
func VerifyHistoricalAuthorization(t finality.Trust, p rules.DirectPolicy, r LeanHistoricalReceipt) (AuthorizedRevision, error) {
	fail := AuthorizedRevision{}
	if p.Key == nil || types.ConfigureRedaction(t.ChainID, p.Key) != nil {
		return fail, protocol.ErrAuth
	}
	tx, height, err := receiptExecution(t, r.Execution, r.CommandIndex)
	if err != nil {
		return fail, err
	}
	var out AuthorizedRevision
	var network protocol.Hash
	var items []protocol.RepairItem
	var parts []chameleon.Opening
	var single []byte
	if protocol.IsRepairBatch(tx.Bytes) {
		c, e := protocol.DecodeRepairBatch(tx.Bytes)
		if e != nil {
			return fail, e
		}
		out.Command, out.TargetHeight, out.Base, out.Previous, out.Next = c.ID(), c.Height, c.Base, c.Previous, c.Next
		network, items, parts = c.Network, c.Items, c.Parts
	} else {
		c, e := protocol.DecodeRepairInput(tx.Bytes)
		if e != nil {
			return fail, e
		}
		out.Command, out.TargetHeight, out.Base, out.Previous, out.Next = c.ID(), c.Height, c.Base, c.Previous, c.Next
		network, parts, single = c.Network, c.Parts, c.TransactionBytes
		pay, e := protocol.DecodeDirectSubmission(single)
		if e != nil || int(c.Input) >= len(pay.Tx.Funding) {
			return fail, protocol.ErrAuth
		}
		items = []protocol.RepairItem{{Output: c.Output, Transaction: c.Transaction, Input: c.Input, Opening: pay.Tx.Funding[c.Input].Opening}}
	}
	result, err := protocol.DecodeRepairResult(tx.Data)
	if err != nil {
		return fail, err
	}
	if network != t.Network || out.TargetHeight >= height || result.Command != out.Command || !result.Applied || digest(r.Body) != out.Next {
		return fail, protocol.ErrAuth
	}
	body, err := decodeBody(r.Body)
	if err != nil {
		return fail, err
	}
	if body.ChainID != t.ChainID || body.Height != out.TargetHeight {
		return fail, protocol.ErrAuth
	}
	for _, x := range items {
		if int(x.Transaction) >= len(body.Txs) {
			return fail, protocol.ErrAuth
		}
		raw := body.Txs[x.Transaction]
		if single != nil && !bytes.Equal(raw, single) {
			return fail, protocol.ErrAuth
		}
		pay, e := protocol.DecodeDirectSubmission(raw)
		if e != nil {
			return fail, e
		}
		if int(x.Input) >= len(pay.Tx.Funding) || pay.Tx.Body.Network != t.Network || pay.Tx.Body.Rules != p.Rules() || pay.Tx.Body.Inputs[x.Input].Output != x.Output {
			return fail, protocol.ErrAuth
		}
		if err = receiptPayment(p, pay); err != nil {
			return fail, err
		}
		funding := pay.Tx.Funding[x.Input]
		if funding.Kind != protocol.ReserveFunding || funding.Ref != protocol.ReserveDebitIdentity(network, x.Output) || funding.Opening != x.Opening || !p.Key.Verify(pay.Tx.FundingContext(int(x.Input), p.Key.KeyID()), funding.ReferenceBytes(), pay.Tx.Commitments[x.Input], funding.Opening) {
			return fail, protocol.ErrAuth
		}
		var organization protocol.Hash
		for _, parent := range pay.InputCertificates {
			if parent.Certificate.Summary.OutputID(parent.Index) == x.Output {
				organization = parent.Certificate.Summary.Issuer
			}
		}
		if organization == (protocol.Hash{}) {
			return fail, protocol.ErrAuth
		}
		decision := protocol.CompensationDecision{Network: network, Output: x.Output, Height: out.TargetHeight, Transaction: x.Transaction, Input: x.Input}
		out.Funding = append(out.Funding, AuthorizedFunding{Output: x.Output, Transaction: x.Transaction, Input: x.Input, ConsumerTx: pay.Tx.ID(), Organization: organization, Amount: pay.Tx.Claims[x.Input].Output.Amount, Decision: decision.ID(), Debit: funding.Ref, Authorized: funding})
	}
	revised, err := types.NewRedactablePartSet(r.Body, types.BlockPartSizeBytes, out.TargetHeight, out.Base+1, parts)
	if err != nil {
		return fail, err
	}
	id := types.BlockID{Hash: body.Hash(), PartSetHeader: revised.Header()}
	if r.OriginalCommit == nil || r.OriginalCommit.Height != out.TargetHeight {
		return fail, protocol.ErrAuth
	}
	if err = t.Validators.VerifyCommitLight(t.ChainID, id, out.TargetHeight, r.OriginalCommit); err != nil {
		return fail, err
	}
	out.ExecutionHeight = height
	out.Revision = out.Base + 1
	out.OriginalBlockID = id
	return out, nil
}

type AuthorizedRevision struct {
	Command                       protocol.Hash
	ExecutionHeight, TargetHeight int64
	Base, Revision                uint64
	Previous, Next                protocol.Hash
	OriginalBlockID               types.BlockID
	Funding                       []AuthorizedFunding
}
type AuthorizedCompensation struct {
	ExecutionHeight int64
	Decision        protocol.CompensationDecision
	Effect          protocol.RepairEffect
}

// VerifyCompensationReceipts authenticates one block once and projects the
// selected economic effects in caller order. Both layouts can share this work.
func VerifyCompensationReceipts(t finality.Trust, b finality.BlockData, indices []uint32) ([]AuthorizedCompensation, error) {
	verified, err := receiptBlock(t, b)
	if err != nil {
		return nil, err
	}
	txs := verified.Transactions()
	out := make([]AuthorizedCompensation, 0, len(indices))
	for _, index := range indices {
		if int(index) >= len(txs) || txs[index].Code != 0 {
			return nil, protocol.ErrAuth
		}
		effect, err := receiptCompensation(t, txs[index], verified.Height())
		if err != nil {
			return nil, err
		}
		out = append(out, effect)
	}
	return out, nil
}

// VerifyHistoricalReceipt verifies one ever-authorized body and its compatibility
// with the original full BlockID. It makes no latest/current/absence claim.
func VerifyHistoricalReceipt(t finality.Trust, p rules.DirectPolicy, r HistoricalReceipt) (AuthorizedRevision, error) {
	return verifyHistorical(t, p, r, true)
}

// VerifyHistoricalProjection gives ordinary overlays the identical exact-version
// authorization check, without requiring the revised part identity property.
func VerifyHistoricalProjection(t finality.Trust, p rules.DirectPolicy, r HistoricalReceipt) (AuthorizedRevision, error) {
	return verifyHistorical(t, p, r, false)
}

// Fresh decoding prevents a caller's cached Block.Hash from authenticating a
// subsequently mutated in-memory header. Result data is checked by VerifyBlock.
func receiptExecution(t finality.Trust, b finality.BlockData, index uint32) (finality.ExecutedTx, int64, error) {
	verified, err := receiptBlock(t, b)
	if err != nil {
		return finality.ExecutedTx{}, 0, err
	}
	txs := verified.Transactions()
	if int(index) >= len(txs) || txs[index].Code != 0 {
		return finality.ExecutedTx{}, 0, protocol.ErrAuth
	}
	return txs[index], verified.Height(), nil
}

func receiptBlock(t finality.Trust, b finality.BlockData) (finality.VerifiedBlock, error) {
	if b.Block == nil {
		return finality.VerifiedBlock{}, protocol.ErrAuth
	}
	pb, err := b.Block.ToProto()
	if err != nil {
		return finality.VerifiedBlock{}, err
	}
	raw, err := pb.Marshal()
	if err != nil {
		return finality.VerifiedBlock{}, err
	}
	fresh, err := decodeBody(raw)
	if err != nil {
		return finality.VerifiedBlock{}, err
	}
	b.Block = fresh
	return finality.VerifyBlock(t, b)
}

// VerifyCompensationReceipt is the independent T1 economic explanation. It
// needs neither a repair command nor any CH revision, and returns the amount
// and consumer identities from the authenticated successful execution effect.
func VerifyCompensationReceipt(t finality.Trust, b finality.BlockData, index uint32) (AuthorizedCompensation, error) {
	fail := AuthorizedCompensation{}
	tx, height, err := receiptExecution(t, b, index)
	if err != nil {
		return fail, err
	}
	return receiptCompensation(t, tx, height)
}

func receiptCompensation(t finality.Trust, tx finality.ExecutedTx, height int64) (AuthorizedCompensation, error) {
	fail := AuthorizedCompensation{}
	c, err := protocol.DecodeCompensationDecision(tx.Bytes)
	if err != nil {
		return fail, err
	}
	result, err := protocol.DecodeCompensationResult(tx.Data)
	if err != nil {
		return fail, err
	}
	if c.Network != t.Network || c.Height >= height || result.Decision != c.ID() || !result.Applied || len(result.Effects) != 1 {
		return fail, protocol.ErrAuth
	}
	e := result.Effects[0]
	if e.Output != c.Output || e.Input != c.Input || e.Debit != protocol.ReserveDebitIdentity(c.Network, c.Output) {
		return fail, protocol.ErrAuth
	}
	return AuthorizedCompensation{ExecutionHeight: height, Decision: c, Effect: e}, nil
}

func verifyHistorical(t finality.Trust, p rules.DirectPolicy, r HistoricalReceipt, identity bool) (AuthorizedRevision, error) {
	fail := AuthorizedRevision{}
	if p.Key == nil || types.ConfigureRedaction(t.ChainID, p.Key) != nil {
		return fail, protocol.ErrAuth
	}
	executed, height, err := receiptExecution(t, r.Execution, r.CommandIndex)
	if err != nil {
		return fail, err
	}
	var out AuthorizedRevision
	var network protocol.Hash
	var items []protocol.RepairItem
	var parts []chameleon.Opening
	var single []byte
	if protocol.IsRepairBatch(executed.Bytes) {
		c, e := protocol.DecodeRepairBatch(executed.Bytes)
		if e != nil {
			return fail, e
		}
		out.Command, out.TargetHeight, out.Base, out.Previous, out.Next = c.ID(), c.Height, c.Base, c.Previous, c.Next
		network, items, parts = c.Network, c.Items, c.Parts
	} else {
		c, e := protocol.DecodeRepairInput(executed.Bytes)
		if e != nil {
			return fail, e
		}
		out.Command, out.TargetHeight, out.Base, out.Previous, out.Next = c.ID(), c.Height, c.Base, c.Previous, c.Next
		network, parts, single = c.Network, c.Parts, c.TransactionBytes
		pay, e := protocol.DecodeDirectSubmission(single)
		if e != nil || int(c.Input) >= len(pay.Tx.Funding) {
			return fail, protocol.ErrAuth
		}
		items = []protocol.RepairItem{{Output: c.Output, Transaction: c.Transaction, Input: c.Input, Opening: pay.Tx.Funding[c.Input].Opening}}
	}
	result, err := protocol.DecodeRepairResult(executed.Data)
	if err != nil {
		return fail, err
	}
	if network != t.Network || out.TargetHeight >= height || result.Command != out.Command || !result.Applied || digest(r.BaseBody) != out.Previous || digest(r.Body) != out.Next {
		return fail, protocol.ErrAuth
	}
	original, err := decodeBody(r.OriginalBody)
	if err != nil {
		return fail, err
	}
	base, err := decodeBody(r.BaseBody)
	if err != nil {
		return fail, err
	}
	next, err := decodeBody(r.Body)
	if err != nil {
		return fail, err
	}
	for _, b := range []*types.Block{original, base, next} {
		if b.ChainID != t.ChainID || b.Height != out.TargetHeight {
			return fail, protocol.ErrAuth
		}
	}
	// Unlike payment projection hashes, the initial fixed-opening part root
	// commits the original full serialized body, including its funding bytes.
	initial, err := types.NewRedactablePartSet(r.OriginalBody, types.BlockPartSizeBytes, out.TargetHeight, 0, nil)
	if err != nil {
		return fail, err
	}
	originalID := types.BlockID{Hash: original.Hash(), PartSetHeader: initial.Header()}
	if r.OriginalCommit == nil || r.OriginalCommit.Height != out.TargetHeight {
		return fail, protocol.ErrAuth
	}
	if err = t.Validators.VerifyCommitLight(t.ChainID, originalID, out.TargetHeight, r.OriginalCommit); err != nil {
		return fail, err
	}
	if out.Base == 0 && !bytes.Equal(r.OriginalBody, r.BaseBody) {
		return fail, protocol.ErrAuth
	}
	if len(original.Txs) != len(base.Txs) {
		return fail, protocol.ErrAuth
	}
	pb, err := base.ToProto()
	if err != nil {
		return fail, err
	}
	payments := map[uint32]protocol.DirectSubmission{}
	for _, x := range items {
		if int(x.Transaction) >= len(base.Txs) {
			return fail, protocol.ErrAuth
		}
		before, e := protocol.DecodeDirectSubmission(original.Txs[x.Transaction])
		if e != nil {
			return fail, e
		}
		pay, ok := payments[x.Transaction]
		if !ok {
			pay, e = protocol.DecodeDirectSubmission(base.Txs[x.Transaction])
			if e != nil {
				return fail, e
			}
		}
		if int(x.Input) >= len(pay.Tx.Funding) || int(x.Input) >= len(before.Tx.Funding) || pay.Tx.ID() != before.Tx.ID() || before.Tx.Body.Network != t.Network || before.Tx.Body.Rules != p.Rules() || before.Tx.Body.Inputs[x.Input].Output != x.Output || pay.Tx.Funding[x.Input].Kind != protocol.OriginalFunding || pay.Tx.Funding[x.Input].Ref != protocol.Hash(x.Output) {
			return fail, protocol.ErrAuth
		}
		if err = receiptPayment(p, before); err != nil {
			return fail, err
		}
		funding := protocol.Funding{Kind: protocol.ReserveFunding, Ref: protocol.ReserveDebitIdentity(network, x.Output), Opening: x.Opening}
		if !p.Key.Verify(pay.Tx.FundingContext(int(x.Input), p.Key.KeyID()), funding.ReferenceBytes(), pay.Tx.Commitments[x.Input], funding.Opening) {
			return fail, protocol.ErrAuth
		}
		var organization protocol.Hash
		for _, parent := range before.InputCertificates {
			if parent.Certificate.Summary.OutputID(parent.Index) == x.Output {
				organization = parent.Certificate.Summary.Issuer
			}
		}
		if organization == (protocol.Hash{}) {
			return fail, protocol.ErrAuth
		}
		decision := protocol.CompensationDecision{Network: network, Output: x.Output, Height: out.TargetHeight, Transaction: x.Transaction, Input: x.Input}
		out.Funding = append(out.Funding, AuthorizedFunding{Output: x.Output, Transaction: x.Transaction, Input: x.Input, ConsumerTx: before.Tx.ID(), Organization: organization, Amount: before.Tx.Claims[x.Input].Output.Amount, Decision: decision.ID(), Debit: funding.Ref, Original: before.Tx.Funding[x.Input], Authorized: funding, OriginalAvailable: true})
		pay.Tx.Funding[x.Input] = funding
		payments[x.Transaction] = pay
	}
	for index, pay := range payments {
		raw, e := pay.MarshalBinary()
		if e != nil {
			return fail, e
		}
		if single != nil && !bytes.Equal(raw, single) {
			return fail, protocol.ErrAuth
		}
		pb.Data.Txs[index] = raw
	}
	reconstructed, err := pb.Marshal()
	if err != nil {
		return fail, err
	}
	if !bytes.Equal(reconstructed, r.Body) {
		return fail, fmt.Errorf("exact authorized transformation: %w", protocol.ErrAuth)
	}
	if identity {
		revised, e := types.NewRedactablePartSet(r.Body, types.BlockPartSizeBytes, out.TargetHeight, out.Base+1, parts)
		if e != nil {
			return fail, e
		}
		if !originalID.Equals(types.BlockID{Hash: next.Hash(), PartSetHeader: revised.Header()}) {
			return fail, protocol.ErrAuth
		}
	}
	out.ExecutionHeight = height
	out.Revision = out.Base + 1
	out.OriginalBlockID = originalID
	return out, nil
}

func receiptPayment(p rules.DirectPolicy, pay protocol.DirectSubmission) error {
	if pay.Tx.VerifyAuth() != nil {
		return protocol.ErrAuth
	}
	cfg, ok := p.Organizations[pay.Tx.Body.Config]
	if !ok || pay.Authorization.Fact != pay.Summary().Fact() || protocol.VerifyQC(pay.Authorization, cfg) != nil {
		return protocol.ErrAuth
	}
	for _, parent := range pay.InputCertificates {
		cfg, ok := p.Organizations[parent.Certificate.Summary.Config]
		if !ok || parent.Certificate.Summary.Network != pay.Tx.Body.Network || parent.Certificate.Summary.Rules != p.Rules() || parent.Certificate.Verify(cfg) != nil {
			return protocol.ErrAuth
		}
	}
	return nil
}
