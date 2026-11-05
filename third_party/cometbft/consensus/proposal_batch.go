package consensus

import (
	"fmt"
	cstypes "github.com/cometbft/cometbft/consensus/types"
	"github.com/cometbft/cometbft/libs/fail"
	"github.com/cometbft/cometbft/libs/operationtrace"
)

const proposalBatchMessages = 16
const proposalBatchBytes = 512 * 1024

// Only the final message of a batch may complete the block and change state.
// Thus WAL state-transition records retain their original position on replay.
func (cs *State) handleInternalMessage(mi msgInfo) {
	batch, next := cs.collectProposalBatch(mi)
	if len(batch) == 1 {
		cs.handleSingleInternal(mi)
	} else {
		end := operationtrace.Start("proposal_batch_wal", cs.Height)
		var err error
		for _, msg := range batch {
			if err = cs.wal.Write(msg); err != nil {
				break
			}
		}
		if err == nil {
			err = cs.wal.FlushAndSync()
		}
		end()
		if err != nil {
			panic(fmt.Sprintf("failed to persist proposal batch: %v", err))
		}
		if operationtrace.Observe != nil {
			cs.Logger.Debug("batched proposal WAL", "height", cs.Height, "round", cs.Round, "messages", len(batch))
		}
		for _, msg := range batch {
			cs.handleMsg(msg)
		}
	}
	// A prefetched boundary message must follow any newStep written by the batch.
	if next != nil {
		cs.handleSingleInternal(*next)
	}
}

// Take only already-ready local parts. Never wait to fill a batch, never read
// past the earliest possible completion, and yield after a bounded prefix.
func (cs *State) collectProposalBatch(first msgInfo) ([]msgInfo, *msgInfo) {
	batch := []msgInfo{first}
	cs.mtx.Lock()
	defer cs.mtx.Unlock()
	if first.PeerID != "" || cs.Step != cstypes.RoundStepPropose {
		return batch, nil
	}
	remaining, size := 0, 0
	if cs.ProposalBlockParts != nil {
		if cs.ProposalBlockParts.Total() <= cs.ProposalBlockParts.Count() {
			return batch, nil
		}
		if cs.Proposal != nil && !cs.ProposalBlockParts.HasHeader(cs.Proposal.BlockID.PartSetHeader) {
			return batch, nil
		}
		remaining = int(cs.ProposalBlockParts.Total() - cs.ProposalBlockParts.Count())
	}
	switch msg := first.Msg.(type) {
	case *ProposalMessage:
		if msg.Proposal == nil || msg.Proposal.Height != cs.Height || msg.Proposal.Round != cs.Round {
			return batch, nil
		}
		if cs.ProposalBlockParts != nil && !cs.ProposalBlockParts.HasHeader(msg.Proposal.BlockID.PartSetHeader) {
			return batch, nil
		}
		if cs.ProposalBlockParts == nil && cs.Proposal == nil {
			remaining = int(msg.Proposal.BlockID.PartSetHeader.Total)
		}
	case *BlockPartMessage:
		if msg.Part == nil || msg.Height != cs.Height || msg.Round != cs.Round {
			return batch, nil
		}
		remaining--
		size = len(msg.Part.Bytes)
	default:
		return batch, nil
	}
	for remaining > 0 && len(batch) < proposalBatchMessages && size < proposalBatchBytes {
		select {
		case next := <-cs.internalMsgQueue:
			part, ok := next.Msg.(*BlockPartMessage)
			if !ok || next.PeerID != "" || part.Part == nil || part.Height != cs.Height || part.Round != cs.Round || size+len(part.Part.Bytes) > proposalBatchBytes {
				return batch, &next
			}
			batch = append(batch, next)
			size += len(part.Part.Bytes)
			// Duplicates/invalid parts count too: this can only shorten the batch.
			remaining--
		default:
			return batch, nil
		}
	}
	return batch, nil
}

// Original single-message path, including the vote crash-injection point.
func (cs *State) handleSingleInternal(mi msgInfo) {
	end := operationtrace.Start("internal_message_wal", cs.Height)
	err := cs.wal.WriteSync(mi)
	end()
	if err != nil {
		panic(fmt.Sprintf("failed to write %v msg to consensus WAL due to %v; check your file system and restart the node", mi, err))
	}
	if _, ok := mi.Msg.(*VoteMessage); ok {
		fail.Fail()
	}
	cs.handleMsg(mi)
}
