package redaction

// Observation is a local experiment probe, not a finality or authorization proof.
// Committed and Materialized deliberately describe different completion points.
type Observation struct {
	Committed, Materialized, IdentityStable, BytesChanged bool
	CommitHeight, TargetHeight                            int64
	Revision                                              uint64
}
