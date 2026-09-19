package rules

import (
	"math"

	"utxo/crypto/chameleon"
	"utxo/internal/state"
	"utxo/protocol"
)

// DirectPolicy belongs to a fixed genesis. Timeouts use committed block time.
type DirectPolicy struct {
	Base           Schedule
	TimeoutSeconds int64
	RepairCost     uint64
	Key            *chameleon.Public
	Organizations  map[protocol.Hash]protocol.OrgConfig
}

func (p DirectPolicy) Rules() protocol.RuleIDs {
	r := p.Base.IDs()
	e := new(protocol.Encoder)
	e.Fixed(r.Accounting[:])
	e.U64(uint64(p.TimeoutSeconds))
	e.U64(p.RepairCost)
	r.Accounting = protocol.Digest("DIRECT_ACCOUNTING_V4_BLOCK_FOLLOWING", e.Data())
	return r
}

type InputCertificate = protocol.InputCertificate
type DirectPayment = protocol.DirectPayment
type VerifiedDirectPayment struct {
	payment DirectPayment
	parents map[protocol.OutputID]InputCertificate
}

const (
	keyDirectCoverage   uint8 = 100
	keyDirectPromise    uint8 = 101
	keyDirectObligation uint8 = 102
	keyDirectPayment    uint8 = 103
	keyDirectRepair     uint8 = 105
	keyDirectGap        uint8 = 106
)
const (
	DirectOpen uint8 = iota
	DirectFulfilled
	DirectRepaired
)

func DirectCreationKey(id protocol.OutputID, instance uint8) []byte {
	return state.Key(state.KeyCreation, id[:], []byte{instance})
}
func DirectSpendKey(id protocol.OutputID, instance uint8) []byte {
	return state.Key(state.KeySpend, id[:], []byte{instance})
}
func DirectObligationKey(id protocol.OutputID) []byte { return state.Key(keyDirectObligation, id[:]) }
func DirectRepairKey(id protocol.OutputID) []byte     { return state.Key(keyDirectRepair, id[:]) }
func DirectGapKey() []byte                            { return state.Key(keyDirectGap) }

func DirectDueKey(deadline int64, id protocol.OutputID) []byte {
	e := new(protocol.Encoder)
	e.U64(uint64(deadline))
	e.Fixed(id[:])
	return state.Key(112, e.Data())
}

type DirectObligation struct {
	AnchorHeight          int64
	Output                protocol.OutputID
	Issuer, Config        protocol.Hash
	Certificate, Consumer protocol.SpendFactID
	Transaction           protocol.TxID
	Input                 uint32
	Amount                uint64
	Deadline              int64
	Status                uint8
}
type DirectRepairTodo struct {
	ID, Debit  protocol.Hash
	Obligation DirectObligation
}
type directPromise struct {
	Certificate protocol.SpendFactID
	Index       uint32
	Status      uint8
}
type CoverageBalance struct{ Original, Paid, Discharged, Remaining, Revision uint64 }

type directCoverage struct {
	Summary protocol.OutputSummary
	Credit  CoverageBalance
}
type directPayment struct {
	Summary    protocol.OutputSummary
	FeeAccount protocol.Hash
	Fee        Escrow
	Pending    uint32
	Settled    bool
}
type DirectPaymentState = directPayment

func PrepareDirectVector(tx protocol.FastTx, p DirectPolicy) (protocol.AdmissionVector, error) {
	t := tx.Body
	if p.Key == nil || p.TimeoutSeconds <= 0 || p.RepairCost == 0 || p.Base.Validate() != nil || t.Rules != p.Rules() || t.Kind != protocol.FastTransfer || t.Fee.Source != protocol.OrgReserve {
		return nil, protocol.ErrRule
	}
	if err := tx.VerifyInitial(p.Key); err != nil {
		return nil, err
	}
	if t.Work.Depth != 1 || t.Work.Ancestors != 1 {
		return nil, protocol.ErrRule
	}
	var cal uint64
	for _, out := range t.Outputs {
		var err error
		cal, err = protocol.Add(cal, out.Amount)
		if err != nil {
			return nil, err
		}
	}
	escrow, err := NewEscrow(t.Fee.Maximum, p.Base.Fee)
	if err != nil {
		return nil, err
	}
	var certified uint64
	for _, in := range t.Inputs {
		if in.Kind == protocol.CertificateInput {
			certified++
		}
	}
	reserve, err := protocol.MulDiv(certified, p.RepairCost, 1)
	if err != nil {
		return nil, err
	}
	minimum, err := protocol.Add(p.Base.Fee.Register, p.Base.Fee.Settle)
	if err != nil {
		return nil, err
	}
	minimum, err = protocol.Add(minimum, p.Base.Fee.Close)
	if err != nil {
		return nil, err
	}
	minimum, err = protocol.Add(minimum, reserve)
	if err != nil || minimum > escrow.Maximum {
		return nil, ErrLimited
	}
	inputCost, err := protocol.MulDiv(uint64(len(t.Inputs)), p.Base.InputExecution, 1)
	if err != nil {
		return nil, err
	}
	outputCost, err := protocol.MulDiv(uint64(len(t.Outputs)), p.Base.OutputExecution, 1)
	if err != nil {
		return nil, err
	}
	exec, err := protocol.Add(p.Base.BaseExecution, inputCost)
	if err != nil {
		return nil, err
	}
	exec, err = protocol.Add(exec, outputCost)
	if err != nil {
		return nil, err
	}
	// Reserve one bounded repair action for each certificate input.
	repairExec, err := protocol.MulDiv(certified, p.Base.BaseExecution, 1)
	if err != nil {
		return nil, err
	}
	exec, err = protocol.Add(exec, repairExec)
	if err != nil {
		return nil, err
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		return nil, err
	}
	retained, err := protocol.Add(p.Base.BaseBytes, uint64(len(raw))+uint64(len(t.Inputs))*protocol.MaxCertificateBytes)
	if err != nil {
		return nil, err
	}
	if exec > uint64(t.Work.Execution) || retained > uint64(t.Work.Bytes) {
		return nil, ErrLimited
	}
	amounts := map[protocol.ResourceKind]uint64{protocol.ResourceCAL: cal, protocol.ResourceFUEL: t.Fee.Maximum, protocol.ResourceExecution: exec, protocol.ResourceBytes: retained, protocol.ResourcePolicy: t.Fee.Maximum}
	if len(t.Admission) != len(amounts) {
		return nil, protocol.ErrRule
	}
	var vector protocol.AdmissionVector
	seen := map[protocol.ResourceKind]bool{}
	for _, ref := range t.Admission {
		cap, ok := amounts[ref.Key.Kind]
		account := t.Certifier
		if ref.Key.Kind == protocol.ResourceFUEL {
			account = t.Fee.Account
		}
		if ref.Key.Kind == protocol.ResourcePolicy {
			account = t.Fee.Policy
		}
		if !ok || seen[ref.Key.Kind] || ref.Key.Account != account || ref.Key.Version != t.Fee.Version {
			return nil, protocol.ErrRule
		}
		seen[ref.Key.Kind] = true
		vector = append(vector, protocol.Allocation{Key: ref.Key, Cap: cap})
	}
	return vector, vector.Validate()
}

func VerifyDirectPayment(payment DirectPayment, p DirectPolicy) (VerifiedDirectPayment, error) {
	// Freeze canonical objects once at the verification boundary.
	raw, err := payment.Tx.MarshalBinary()
	if err != nil {
		return VerifiedDirectPayment{}, err
	}
	tx, err := protocol.DecodeFastTx(raw)
	if err != nil {
		return VerifiedDirectPayment{}, err
	}
	payment.Tx = tx
	freeze := func(c protocol.OutputCertificate) (protocol.OutputCertificate, error) {
		b, err := c.MarshalBinary()
		if err != nil {
			return c, err
		}
		return protocol.DecodeOutputCertificate(b)
	}
	payment.Certificate, err = freeze(payment.Certificate)
	if err != nil {
		return VerifiedDirectPayment{}, err
	}
	vector, err := PrepareDirectVector(tx, p)
	if err != nil {
		return VerifiedDirectPayment{}, err
	}
	cfg, ok := p.Organizations[tx.Body.Config]
	if !ok || payment.Certificate.Verify(cfg) != nil || payment.Certificate.Summary.Fact() != protocol.SummaryFor(tx, vector).Fact() {
		return VerifiedDirectPayment{}, protocol.ErrAuth
	}
	if len(payment.InputCertificates) > protocol.MaxInputs {
		return VerifiedDirectPayment{}, protocol.ErrRule
	}
	parents := make(map[protocol.OutputID]InputCertificate, len(payment.InputCertificates))
	frozen := make([]InputCertificate, 0, len(payment.InputCertificates))
	for _, parent := range payment.InputCertificates {
		parent.Certificate, err = freeze(parent.Certificate)
		if err != nil {
			return VerifiedDirectPayment{}, err
		}
		c := parent.Certificate
		cfg, ok := p.Organizations[c.Summary.Config]
		if !ok || c.Verify(cfg) != nil || c.Summary.Network != tx.Body.Network || c.Summary.Rules != p.Rules() || int(parent.Index) >= len(c.Summary.Outputs) {
			return VerifiedDirectPayment{}, protocol.ErrAuth
		}
		id := c.Summary.OutputID(parent.Index)
		if _, duplicate := parents[id]; duplicate {
			return VerifiedDirectPayment{}, protocol.ErrRule
		}
		parents[id] = parent
		frozen = append(frozen, parent)
	}
	payment.InputCertificates = frozen
	for i, in := range tx.Body.Inputs {
		if in.Kind != protocol.CertificateInput {
			continue
		}
		parent, ok := parents[in.Output]
		if !ok || in.Evidence != protocol.Hash(parent.Certificate.QC.Fact) || parent.Certificate.VerifyOutput(p.Organizations[parent.Certificate.Summary.Config], parent.Index, tx.Claims[i].Output) != nil {
			return VerifiedDirectPayment{}, protocol.ErrAuth
		}
	}
	return VerifiedDirectPayment{payment: payment, parents: parents}, nil
}

type directEval struct {
	o          *state.Overlay
	policy     DirectPolicy
	network    protocol.Hash
	resultData protocol.ExecutionResult
}

func newDirectEval(v state.ReadView, p DirectPolicy, network protocol.Hash) *directEval {
	return &directEval{o: state.NewOverlay(v), policy: p, network: network}
}
func (e *directEval) result() state.Transition {
	raw, _ := e.resultData.MarshalBinary()
	return state.Transition{Changes: e.o.Changes(), Data: raw}
}

func (e *directEval) grant(s protocol.OutputSummary, index int) (state.Grant, error) {
	a := s.Admission[index]
	g, ok, err := state.Load[state.Grant](e.o, state.Key(state.KeyGrant, a.Key.Encode()))
	if err != nil {
		return g, err
	}
	if !ok || g.ID != s.Grants[index] || g.Organization != s.Config || g.Key != a.Key {
		return g, protocol.ErrAuth
	}
	return g, nil
}
func (e *directEval) register(c protocol.OutputCertificate) error {
	s := c.Summary
	fact := c.QC.Fact
	key := state.Key(keyDirectCoverage, fact[:])
	if _, found, err := state.Load[directCoverage](e.o, key); err != nil {
		return err
	} else if found {
		return nil
	}
	var receipt CoverageBalance
	for i, a := range s.Admission {
		if a.Key.Kind == protocol.ResourceCAL {
			g, err := e.grant(s, i)
			if err != nil {
				return err
			}
			if err = updateUsage(e.o, a.Key, a.Cap, 0, g.Amount, true); err != nil {
				return err
			}
			receipt = CoverageBalance{Original: a.Cap, Remaining: a.Cap}
		}
	}
	if receipt.Original == 0 {
		return protocol.ErrRule
	}
	for i := range s.Outputs {
		id := s.OutputID(uint32(i))
		key := state.Key(keyDirectPromise, id[:])
		old, found, err := state.Load[directPromise](e.o, key)
		if err != nil {
			return err
		}
		if found && (old.Certificate != fact || old.Index != uint32(i)) {
			return ErrConflict
		}
		if !found {
			if err = state.Put(e.o, key, directPromise{Certificate: fact, Index: uint32(i)}); err != nil {
				return err
			}
		}
	}
	return state.Put(e.o, key, directCoverage{Summary: s, Credit: receipt})
}

func (e *directEval) completeOutput(id protocol.OutputID, paid bool) error {
	key := state.Key(keyDirectPromise, id[:])
	promise, ok, err := state.Load[directPromise](e.o, key)
	if err != nil {
		return err
	}
	if !ok {
		return ErrMissing
	}
	if promise.Status != DirectOpen {
		return nil
	}
	coverageKey := state.Key(keyDirectCoverage, promise.Certificate[:])
	coverage, ok, err := state.Load[directCoverage](e.o, coverageKey)
	if err != nil {
		return err
	}
	if !ok {
		return ErrMissing
	}
	amount := coverage.Summary.Outputs[promise.Index].Amount
	r := &coverage.Credit
	r.Remaining, err = protocol.Sub(r.Remaining, amount)
	if err != nil {
		return err
	}
	spent := uint64(0)
	promise.Status = DirectFulfilled
	if paid {
		r.Paid, err = protocol.Add(r.Paid, amount)
		spent = amount
		promise.Status = DirectRepaired
	} else {
		r.Discharged, err = protocol.Add(r.Discharged, amount)
	}
	if err != nil {
		return err
	}
	r.Revision++
	for i, a := range coverage.Summary.Admission {
		if a.Key.Kind == protocol.ResourceCAL {
			g, err := e.grant(coverage.Summary, i)
			if err != nil {
				return err
			}
			if err = updateUsage(e.o, a.Key, amount, spent, g.Amount, false); err != nil {
				return err
			}
		}
	}
	if err = state.Put(e.o, key, promise); err != nil {
		return err
	}
	if err = state.Put(e.o, coverageKey, coverage); err != nil {
		return err
	}

	return nil
}

func (e *directEval) feeStage(p *directPayment, action FeeAction) error {
	old := p.Fee
	next, err := old.Complete(action)
	if err != nil {
		return err
	}
	beneficiary := p.Summary.Issuer
	if action == FeeSettle {
		beneficiary = protocol.Digest("COMMITTEE_REWARDS", e.network[:])
	}
	if err = changeAmount(e.o, state.Key(state.KeyReward, beneficiary[:]), next.Rewards-old.Rewards, false); err != nil {
		return err
	}
	if err = changeAmount(e.o, state.Key(state.KeyBurned), next.Burned-old.Burned, false); err != nil {
		return err
	}
	if err = changeAmount(e.o, AccountKey(p.FeeAccount, protocol.AssetFUEL), next.Refunded-old.Refunded, false); err != nil {
		return err
	}
	p.Fee = next
	return nil
}

func (e *directEval) closeFee(p *directPayment) error {
	if p.Pending != 0 || p.Fee.Closed {
		return nil
	}
	if err := e.feeStage(p, FeeClose); err != nil {
		return err
	}
	paid, err := protocol.Add(p.Fee.Rewards, p.Fee.Burned)
	if err != nil {
		return err
	}
	for i, a := range p.Summary.Admission {
		if a.Key.Kind == protocol.ResourceCAL {
			continue
		}
		spent := uint64(0)
		if a.Key.Kind == protocol.ResourceFUEL || a.Key.Kind == protocol.ResourcePolicy {
			spent = paid
		}
		g, err := e.grant(p.Summary, i)
		if err != nil {
			return err
		}
		if err = updateUsage(e.o, a.Key, a.Cap, spent, g.Amount, false); err != nil {
			return err
		}
	}

	return nil
}

func (e *directEval) endObligation(ob *DirectObligation, paid bool) error {
	if ob.Status != DirectOpen {
		return nil
	}
	if err := e.completeOutput(ob.Output, paid); err != nil {
		return err
	}
	ob.Status = DirectFulfilled
	if paid {
		ob.Status = DirectRepaired
	}
	if err := state.Put(e.o, DirectObligationKey(ob.Output), *ob); err != nil {
		return err
	}
	e.o.Apply([]state.Change{{Key: DirectDueKey(ob.Deadline, ob.Output), Delete: true}})
	if err := changeAmount(e.o, DirectGapKey(), ob.Amount, true); err != nil {
		return err
	}
	key := state.Key(keyDirectPayment, ob.Consumer[:])
	p, ok, err := state.Load[directPayment](e.o, key)
	if err != nil {
		return err
	}
	if !ok || p.Pending == 0 {
		return ErrAccounting
	}
	p.Pending--
	if paid {
		p.Fee.Held, err = protocol.Sub(p.Fee.Held, e.policy.RepairCost)
		if err != nil {
			return err
		}
		p.Fee.Rewards, err = protocol.Add(p.Fee.Rewards, e.policy.RepairCost)
		if err != nil {
			return err
		}
		beneficiary := protocol.Digest("COMMITTEE_REWARDS", e.network[:])
		if err = changeAmount(e.o, state.Key(state.KeyReward, beneficiary[:]), e.policy.RepairCost, false); err != nil {
			return err
		}
	}
	if err = e.closeFee(&p); err != nil {
		return err
	}
	return state.Put(e.o, key, p)
}

func EvaluateDirectPayment(v state.ReadView, verified VerifiedDirectPayment, policy DirectPolicy, now int64) (state.Transition, error) {
	return EvaluateDirectPaymentAt(v, verified, policy, now, 0)
}

func EvaluateDirectPaymentAt(v state.ReadView, verified VerifiedDirectPayment, policy DirectPolicy, now, height int64) (state.Transition, error) {
	pay := verified.payment
	tx := pay.Tx
	t := tx.Body
	c := pay.Certificate
	fact := c.QC.Fact
	if fact == (protocol.SpendFactID{}) || t.Rules != policy.Rules() || now <= 0 || policy.TimeoutSeconds <= 0 || now > math.MaxInt64-policy.TimeoutSeconds {
		return state.Transition{}, protocol.ErrRule
	}
	e := newDirectEval(v, policy, t.Network)
	key := state.Key(keyDirectPayment, fact[:])
	old, found, err := state.Load[directPayment](e.o, key)
	if err != nil {
		return state.Transition{}, err
	}
	if found && old.Settled {
		return state.Transition{}, nil
	}
	if prior, found, err := state.Load[protocol.TxID](e.o, state.Key(state.KeyIntent, t.Intent[:])); err != nil {
		return state.Transition{}, err
	} else if found && prior != tx.ID() {
		return state.Transition{}, ErrConflict
	}
	if err = e.register(c); err != nil {
		return state.Transition{}, err
	}
	p := directPayment{Summary: c.Summary, FeeAccount: t.Fee.Account}
	e.resultData.Applied = true
	for i, a := range c.Summary.Admission {
		if a.Key.Kind == protocol.ResourceCAL {
			continue
		}
		g, err := e.grant(c.Summary, i)
		if err != nil {
			return state.Transition{}, err
		}
		if a.Key.Kind == protocol.ResourcePolicy && g.Subject != t.Subject {
			return state.Transition{}, protocol.ErrAuth
		}
		if err = updateUsage(e.o, a.Key, a.Cap, 0, g.Amount, true); err != nil {
			return state.Transition{}, err
		}
	}
	if err = changeAmount(e.o, AccountKey(t.Fee.Account, protocol.AssetFUEL), t.Fee.Maximum, true); err != nil {
		return state.Transition{}, err
	}
	p.Fee, err = NewEscrow(t.Fee.Maximum, policy.Base.Fee)
	if err != nil {
		return state.Transition{}, err
	}
	if err = e.feeStage(&p, FeeRegister); err != nil {
		return state.Transition{}, err
	}
	var incoming, outgoing uint64
	for i, in := range t.Inputs {
		claim := tx.Claims[i]
		spent, _, err := state.Load[state.Spend](e.o, DirectSpendKey(in.Output, claim.Instance))
		if err != nil {
			return state.Transition{}, err
		}
		if spent.Consumed != (protocol.SpendFactID{}) {
			return state.Transition{}, ErrConflict
		}
		creation, exists, err := state.Load[state.Creation](e.o, DirectCreationKey(in.Output, claim.Instance))
		if err != nil {
			return state.Transition{}, err
		}
		if exists && creation.Final {
			if creation.Output != claim.Output || (in.Kind == protocol.FinalInput && in.Evidence != creation.Fact) || (in.Kind == protocol.CertificateInput && in.Evidence != creation.Source) {
				return state.Transition{}, protocol.ErrAuth
			}
		} else {
			parent, ok := verified.parents[in.Output]
			if !ok || claim.Instance != 0 || in.Kind != protocol.CertificateInput {
				return state.Transition{}, ErrMissing
			}
			if err = e.register(parent.Certificate); err != nil {
				return state.Transition{}, err
			}
			if _, exists, err := state.Load[DirectObligation](e.o, DirectObligationKey(in.Output)); err != nil {
				return state.Transition{}, err
			} else if exists {
				return state.Transition{}, ErrConflict
			}
			ob := DirectObligation{Output: in.Output, Issuer: parent.Certificate.Summary.Issuer, Config: parent.Certificate.Summary.Config, Certificate: parent.Certificate.QC.Fact, Consumer: fact, Transaction: tx.ID(), Input: uint32(i), Amount: claim.Output.Amount, Deadline: now + policy.TimeoutSeconds}
			if height > 0 {
				ob.Deadline = 0
				ob.AnchorHeight = height + 1
			}
			if err = state.Put(e.o, DirectObligationKey(in.Output), ob); err != nil {
				return state.Transition{}, err
			}
			deadlineKey := DirectDueKey(ob.Deadline, ob.Output)
			if height > 0 {
				deadlineKey = DirectAnchorKey(height+1, ob.Output)
			}
			if err = state.Put(e.o, deadlineKey, ob); err != nil {
				return state.Transition{}, err
			}
			if err = changeAmount(e.o, DirectGapKey(), ob.Amount, false); err != nil {
				return state.Transition{}, err
			}
			p.Pending++
			e.resultData.MissingInputs = append(e.resultData.MissingInputs, uint32(i))
		}
		incoming, err = protocol.Add(incoming, claim.Output.Amount)
		if err != nil {
			return state.Transition{}, err
		}
		if err = state.Put(e.o, DirectSpendKey(in.Output, claim.Instance), state.Spend{Consumed: fact}); err != nil {
			return state.Transition{}, err
		}
	}
	for _, out := range t.Outputs {
		outgoing, err = protocol.Add(outgoing, out.Amount)
		if err != nil {
			return state.Transition{}, err
		}
	}
	if incoming != outgoing {
		return state.Transition{}, ErrAccounting
	}
	p.Settled = true
	if err = e.feeStage(&p, FeeSettle); err != nil {
		return state.Transition{}, err
	}
	if err = e.closeFee(&p); err != nil {
		return state.Transition{}, err
	}
	if err = state.Put(e.o, key, p); err != nil {
		return state.Transition{}, err
	}
	for i, out := range t.Outputs {
		id := c.Summary.OutputID(uint32(i))
		ob, exists, err := state.Load[DirectObligation](e.o, DirectObligationKey(id))
		if err != nil {
			return state.Transition{}, err
		}
		instance := uint8(0)
		if exists {
			if ob.Certificate != fact || ob.Amount != out.Amount {
				return state.Transition{}, protocol.ErrAuth
			}
			if ob.Status == DirectRepaired {
				instance = 1
			} else if ob.Status == DirectOpen {
				if err = e.endObligation(&ob, false); err != nil {
					return state.Transition{}, err
				}
			}
		}
		if err = e.completeOutput(id, false); err != nil {
			return state.Transition{}, err
		}
		if instance == 1 {
			e.resultData.LateOutputs = append(e.resultData.LateOutputs, uint32(i))
		}
		created := protocol.CreationIdentity(t.Network, tx.ID(), uint32(i), instance)
		if err = state.Put(e.o, DirectCreationKey(id, instance), state.Creation{Output: out, Fact: created, Source: protocol.Hash(fact), Final: true}); err != nil {
			return state.Transition{}, err
		}

	}
	if err = state.Put(e.o, state.Key(state.KeyIntent, t.Intent[:]), tx.ID()); err != nil {
		return state.Transition{}, err
	}
	return e.result(), nil
}

// EvaluateDirectCompensation is the accounting substep of an authenticated
// RepairInput. The committee must validate its byte-level repair plan first and
// commit that plan with these changes; this function is not an RPC command.
func EvaluateDirectCompensation(v state.ReadView, id protocol.OutputID, policy DirectPolicy, now int64) (state.Transition, error) {
	ob, found, err := state.Load[DirectObligation](v, DirectObligationKey(id))
	if err != nil {
		return state.Transition{}, err
	}
	if !found {
		return state.Transition{}, ErrMissing
	}
	if ob.Status != DirectOpen {
		return state.Transition{}, nil
	}
	if ob.Deadline == 0 || now < ob.Deadline {
		return state.Transition{}, ErrLimited
	}
	cfg, ok := policy.Organizations[ob.Config]
	if !ok || cfg.Org != ob.Issuer {
		return state.Transition{}, protocol.ErrAuth
	}
	e := newDirectEval(v, policy, cfg.Network)
	e.resultData.Applied = true
	if err = changeAmount(e.o, AccountKey(ob.Issuer, protocol.AssetCAL), ob.Amount, true); err != nil {
		return state.Transition{}, err
	}
	if err = e.endObligation(&ob, true); err != nil {
		return state.Transition{}, err
	}
	idHash := protocol.RepairIdentity(cfg.Network, id)
	todo := DirectRepairTodo{ID: idHash, Debit: protocol.ReserveDebitIdentity(cfg.Network, id), Obligation: ob}
	if err = state.Put(e.o, DirectRepairKey(id), todo); err != nil {
		return state.Transition{}, err
	}
	return e.result(), nil
}
