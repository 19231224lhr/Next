package rules

import "utxo/protocol"

// CheckDirectCredits checks the identities/caps of already-authenticated facts.
// Missing or unfinished receipts mean pending; duplicates never count twice.
func CheckDirectCredits(s protocol.OutputSummary, facts []protocol.FinalFact) (bool, error) {
	expected := map[protocol.ResourceKey]uint64{}
	for _, a := range s.Admission {
		expected[a.Key] = a.Cap
	}
	seen := map[protocol.ResourceKey]bool{}
	done := 0
	for _, f := range facts {
		r, err := protocol.DecodeCredit(f.Payload)
		if err != nil {
			return false, err
		}
		cap, ok := expected[r.Resource]
		if !ok || seen[r.Resource] || f.Kind != protocol.FactCredit || f.Key != r.Key() || f.Network != s.Network || f.Rules != s.Rules || f.Revision != r.Revision || r.Spend != s.Fact() || r.Original != cap {
			return false, protocol.ErrAuth
		}
		seen[r.Resource] = true
		if r.Remaining == 0 {
			done++
		}
	}
	return done == len(expected), nil
}
