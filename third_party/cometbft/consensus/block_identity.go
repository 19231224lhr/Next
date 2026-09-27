package consensus

import "github.com/cometbft/cometbft/types"

// The stable hash omits replaceable funding bytes. Live consensus identifies
// original bytes by the full BlockID; historical adaptation is a separate path.
func matchesBlockID(block *types.Block, parts *types.PartSet, id types.BlockID) bool {
	return block != nil && parts != nil && block.HashesTo(id.Hash) && parts.HasHeader(id.PartSetHeader)
}

func (cs *State) hasProposalBlock() bool {
	return cs.Proposal != nil && matchesBlockID(cs.ProposalBlock, cs.ProposalBlockParts, cs.Proposal.BlockID)
}
