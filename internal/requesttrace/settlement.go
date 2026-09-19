package requesttrace

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sort"
	"sync"
	"time"
	"utxo/protocol"
)

const DeliveryTimeHeader = "X-UTXO-Delivery-Unix-Ns"
const DeliverySourceHeader = "X-UTXO-Delivery-Source"

// Settlement is opt-in local diagnostics, not consensus state or financial proof.
var Settlement = func() *SettlementRecorder {
	if os.Getenv("UTXO_SETTLEMENT_TRACE") != "1" {
		return nil
	}
	return NewSettlementRecorder(2048)
}()

type SettlementTiming struct {
	FinalCheckStartUnixNS int64
	FinalCheckDoneUnixNS  int64
	PreparedUnixNS        int64
	PreparedHeight        int64
	ProposalSeenUnixNS    int64
	ProposalHeight        int64
	Attempt               string
	Spend                 string
	Sender                string
	Height                int64
	DeliveredUnixNS       int64
	ReceivedUnixNS        int64
	AcceptedUnixNS        int64
	ExecuteStartUnixNS    int64
	ExecuteDoneUnixNS     int64
	FinalizeDoneUnixNS    int64
	CommitStartUnixNS     int64
	CommittedUnixNS       int64
}

type SettlementRecorder struct {
	mu      sync.Mutex
	limit   int
	next    int
	order   []string
	records map[string]*SettlementTiming
}

func NewSettlementRecorder(limit int) *SettlementRecorder {
	if limit <= 0 {
		return nil
	}
	return &SettlementRecorder{limit: limit, records: make(map[string]*SettlementTiming)}
}
func (r *SettlementRecorder) get(raw []byte) *SettlementTiming {
	sum := sha256.Sum256(raw)
	id := hex.EncodeToString(sum[:])
	if event := r.records[id]; event != nil {
		return event
	}
	body := raw
	if submission, err := protocol.DecodeSubmission(raw); err == nil {
		body = submission.Body
	}
	var fact protocol.SpendFactID
	if cert, err := protocol.DecodeCertificate(body); err == nil {
		fact = cert.QC.Fact
	} else if payment, err := protocol.DecodeDirectPayment(body); err == nil {
		fact = payment.Certificate.QC.Fact
	} else {
		return nil
	}
	event := &SettlementTiming{Attempt: id, Spend: protocol.Hash(fact).String()}
	if len(r.order) < r.limit {
		r.order = append(r.order, id)
	} else {
		delete(r.records, r.order[r.next])
		r.order[r.next] = id
		r.next = (r.next + 1) % r.limit
	}
	r.records[id] = event
	return event
}
func (r *SettlementRecorder) Receive(raw []byte, sent, received int64, source string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	event := r.get(raw)
	if event == nil || event.ReceivedUnixNS != 0 {
		return
	}
	if len(source) > 32 {
		source = ""
	}
	event.DeliveredUnixNS = sent
	event.ReceivedUnixNS = received
	event.Sender = source
}
func (r *SettlementRecorder) Command(raw []byte, stage string, height int64) {
	if r == nil {
		return
	}
	now := time.Now().UnixNano()
	r.mu.Lock()
	defer r.mu.Unlock()
	event := r.get(raw)
	if event == nil {
		return
	}
	switch stage {
	case "final_check_start":
		if event.FinalCheckStartUnixNS == 0 {
			event.FinalCheckStartUnixNS = now
		}
	case "final_check_done":
		if event.FinalCheckDoneUnixNS == 0 {
			event.FinalCheckDoneUnixNS = now
		}
	case "prepared":
		if event.PreparedUnixNS == 0 {
			event.PreparedUnixNS = now
			event.PreparedHeight = height
		}
	case "proposal_seen":
		if event.ProposalSeenUnixNS == 0 {
			event.ProposalSeenUnixNS = now
			event.ProposalHeight = height
		}
	case "accepted":
		if event.AcceptedUnixNS == 0 {
			event.AcceptedUnixNS = now
		}
	case "execute_start":
		if event.ExecuteStartUnixNS == 0 {
			event.Height = height
			event.ExecuteStartUnixNS = now
		}
	case "execute_done":
		if event.Height == height && event.ExecuteDoneUnixNS == 0 {
			event.ExecuteDoneUnixNS = now
		}
	}
}
func (r *SettlementRecorder) Block(height int64, stage string) {
	if r == nil {
		return
	}
	now := time.Now().UnixNano()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.records {
		if event.Height != height {
			continue
		}
		switch stage {
		case "finalize_done":
			if event.FinalizeDoneUnixNS == 0 {
				event.FinalizeDoneUnixNS = now
			}
		case "commit_start":
			if event.CommitStartUnixNS == 0 {
				event.CommitStartUnixNS = now
			}
		case "commit_done":
			if event.CommittedUnixNS == 0 {
				event.CommittedUnixNS = now
			}
		}
	}
}
func (r *SettlementRecorder) ForSpend(spend string) []SettlementTiming {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var events []SettlementTiming
	for _, event := range r.records {
		if event.Spend == spend {
			events = append(events, *event)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Attempt < events[j].Attempt })
	return events
}
