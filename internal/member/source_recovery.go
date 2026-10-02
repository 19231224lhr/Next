package member

import (
	"time"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

// recoverExposedSources runs in the verified block transaction. Only an
// original signer with matching retained material can create a recovery job.
// The existing relay performs network work after this transaction commits.
func (m *Member) recoverExposedSources(o *state.Overlay, inputs []protocol.InputCertificate) error {
	if m.cfg.DisableSourceRecovery {
		return nil
	}
	for _, input := range inputs {
		cert := input.Certificate
		if cert.Summary.Config != m.cfg.Organization.Hash() {
			continue
		}
		fact := cert.QC.Fact
		if _, found, err := state.Load[bool](o, state.Key(state.KeyObserved, fact[:])); err != nil {
			return err
		} else if found {
			continue
		}
		outkey := state.Key(state.KeyOutbox, fact[:])
		if _, found, err := state.Load[state.Outbox](o, outkey); err != nil {
			return err
		} else if found {
			continue // Duplicate exposure cannot postpone an existing retry.
		}
		a, found, err := state.Load[state.Approval](o, state.Key(state.KeyApproval, fact[:]))
		if err != nil {
			return err
		}
		if !found || a.Direct == nil {
			continue
		}
		if a.Fact != fact || protocol.SummaryFor(*a.Direct, a.Admission).Fact() != cert.Summary.Fact() {
			return protocol.ErrAuth
		}
		payment := protocol.DirectPayment{Tx: *a.Direct, Certificate: cert, InputCertificates: a.DirectParents}
		if _, err := rules.VerifyDirectPayment(payment, *m.direct); err != nil {
			return err
		}
		raw, err := payment.MarshalBinary()
		if err != nil {
			return err
		}
		// Stagger original signers without waiting for INSTALL or another QC.
		pending := state.Outbox{Fact: fact, Origin: payment.Tx.Body.Certifier, Certificate: raw,
			NextSubmitUnixNS: time.Now().Add(time.Duration(m.cfg.Index) * 250 * time.Millisecond).UnixNano()}
		if err := state.Put(o, outkey, pending); err != nil {
			return err
		}
	}
	return nil
}
