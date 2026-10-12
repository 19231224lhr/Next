package protocol

// VerifiedOutputs owns the output commitments authenticated by a certificate.
// Its zero value cannot authorize an output. It carries no ledger-state checks.
type VerifiedOutputs struct {
	network Hash
	outputs []OutputCommitment
}

// VerifyOutputs checks the complete certificate once and snapshots its outputs.
// The snapshot is independent of later changes to the caller's certificate.
func (c OutputCertificate) VerifyOutputs(cfg OrgConfig) (VerifiedOutputs, error) {
	if err := c.Verify(cfg); err != nil {
		return VerifiedOutputs{}, err
	}
	return VerifiedOutputs{
		network: c.Summary.Network,
		outputs: append([]OutputCommitment(nil), c.Summary.Outputs...),
	}, nil
}

// VerifyOutput checks an output against previously authenticated commitments,
// without rechecking the certificate's quorum signatures.
func (v VerifiedOutputs) VerifyOutput(index uint32, o Output) error {
	return verifyOutputCommitment(v.network, v.outputs, index, o)
}

func verifyOutputCommitment(network Hash, outputs []OutputCommitment, index uint32, o Output) error {
	if uint64(index) >= uint64(len(outputs)) || o.Asset != AssetCAL || o.Recipient.Verify(network) != nil || outputs[index] != (OutputCommitment{Digest: OutputDigest(o), Amount: o.Amount}) {
		return ErrAuth
	}
	return nil
}
