package protocol

import "crypto/ed25519"

const ApprovalFeedPageSize = 8

// ApprovedDirect is a committed GM approval with everything needed for public
// submission. Reading it does not ask the GM to approve or reserve again.
type ApprovedDirect struct {
	Request  DirectRequest
	Approval DirectApproval
}
type ApprovalPage struct {
	Items []ApprovedDirect
	Next  []byte
}

func ValidateDirectApproval(org OrgConfig, request DirectRequest, a DirectApproval, index int) error {
	if index < 0 || index >= len(org.Members) || int(a.Vote.Member) != index || request.Tx.Body.Config != org.Hash() {
		return ErrAuth
	}
	expected := SummaryFor(request.Tx, a.Summary.Admission)
	fact := a.Summary.Fact()
	signed := Digest("SPEND_VOTE", fact[:])
	if a.Summary.Validate() != nil || fact != expected.Fact() || !ed25519.Verify(org.Members[index][:], signed[:], a.Vote.Signature[:]) {
		return ErrAuth
	}
	return nil
}
