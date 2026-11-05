package main

// This is a fixture-specific experiment observation, never an authorization
// decision. Members still run the complete approval path on every request.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"
	"utxo/internal/member"
	"utxo/internal/state"
	"utxo/internal/transport"
	"utxo/protocol"
)

type chainGateSample struct {
	Member, Poll              int
	StartedUnixNS, DoneUnixNS int64
	Status                    member.DirectStatus
	Error                     string `json:",omitempty"`
}
type chainMemberGate struct {
	Parent                    protocol.SpendFactID
	RequiresParentObservation bool
	GenesisFeeValidated       bool
	StartedUnixNS, DoneUnixNS int64
	MS                        float64
	Polls, MaxMemberPolls     int
	Quorum                    []int
	Samples                   []chainGateSample
}

func waitChainMembers(ctx context.Context, parent protocol.SpendFactID, requireParent bool, load func(context.Context, int, protocol.SpendFactID) (member.DirectStatus, error)) (gate chainMemberGate, err error) {
	started := time.Now()
	gate.Parent, gate.RequiresParentObservation = parent, requireParent
	gate.StartedUnixNS = started.UnixNano()
	defer func() { gate.DoneUnixNS = time.Now().UnixNano(); gate.MS = float64(time.Since(started)) / 1e6 }()
	if err = ctx.Err(); err != nil {
		return gate, err
	}
	watch, cancel := context.WithCancel(ctx)
	defer cancel()
	replies := make(chan chainGateSample, 4)
	for i := 0; i < 4; i++ {
		go func(i int) {
			for poll := 1; ; poll++ {
				s := chainGateSample{Member: i, Poll: poll, StartedUnixNS: time.Now().UnixNano()}
				var e error
				s.Status, e = load(watch, i, parent)
				s.DoneUnixNS = time.Now().UnixNano()
				if e != nil {
					s.Error = e.Error()
				}
				select {
				case <-watch.Done():
					return
				case replies <- s:
				}
				if e == nil && (!requireParent || s.Status.Observed) {
					return
				}
				if budgetPause(watch, 5*time.Millisecond) != nil {
					return
				}
			}
		}(i)
	}
	for {
		select {
		case <-ctx.Done():
			return gate, ctx.Err()
		case s := <-replies:
			gate.Samples = append(gate.Samples, s)
			gate.Polls++
			gate.MaxMemberPolls = max(gate.MaxMemberPolls, s.Poll)
			if s.Error == "" && (!requireParent || s.Status.Observed) {
				gate.Quorum = append(gate.Quorum, s.Member)
				if len(gate.Quorum) == 3 {
					if err = ctx.Err(); err != nil {
						return gate, err
					}
					return gate, nil
				}
			}
		}
	}
}

func validateChainWaitInputs(req protocol.DirectRequest, parent *chainHop, genesis []state.OriginOutput, usedFees map[protocol.OutputID]bool) error {
	tx := req.Tx
	if len(tx.Body.Inputs) != 1 || len(tx.Claims) != 1 || tx.Body.Inputs[0].Kind != protocol.FinalInput || tx.Claims[0].Instance != 0 || len(req.InputCertificates) != 0 {
		return fmt.Errorf("member wait requires one unrecovered final principal")
	}
	in, claim := tx.Body.Inputs[0], tx.Claims[0].Output
	if parent != nil {
		if in.Output != parent.Output || in.Evidence != protocol.CreationIdentity(tx.Body.Network, parent.Tx, 0, 0) || claim != parent.OutputData {
			return fmt.Errorf("principal does not match immediate parent output")
		}
	} else {
		found := false
		for _, g := range genesis {
			if g.ID == in.Output && g.Fact == in.Evidence && g.Output == claim {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("initial principal does not match genesis")
		}
	}
	if tx.Body.Fee.Source != protocol.OwnerFinalUTXO || len(tx.Body.Fee.Inputs) != 1 {
		return fmt.Errorf("member wait requires independent genesis FUEL")
	}
	fee := tx.Body.Fee.Inputs[0]
	if fee.Kind != protocol.FinalInput || usedFees[fee.Output] {
		return fmt.Errorf("fee input is nonfinal or reused")
	}
	for _, g := range genesis {
		if g.ID != fee.Output {
			continue
		}
		if g.Fact != fee.Evidence || g.Output.Asset != protocol.AssetFUEL || g.Output.Amount < tx.Body.Fee.Maximum || g.Output.Recipient.Route.Kind != protocol.OrgRoute || g.Output.Recipient.Route.Org != tx.Body.Certifier {
			return fmt.Errorf("fee input does not match funded genesis FUEL")
		}
		for _, a := range tx.Auth {
			if a.Owner == g.Output.Recipient.Owner {
				return nil
			}
		}
		return fmt.Errorf("genesis fee owner not included in authorization")
	}
	return fmt.Errorf("fee input absent from genesis")
}

type chainHTTPAttempt struct {
	Attempt                   int
	RequestSHA256             string
	StartedUnixNS, DoneUnixNS int64
	ServiceMS                 float64
	HTTPStatus                int
	ResponseBody              string `json:",omitempty"`
	Error                     string `json:",omitempty"`
	RetrySleepMS              float64
}

// Match faultSend's existing retry policy and 50ms pause; record literal
// gateway responses without inferring which member check failed.
func measuredFaultSend(ctx context.Context, client *http.Client, url string, raw []byte, beforeFirstSend func(time.Time)) (b []byte, attempts []chainHTTPAttempt, err error) {
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	for number := 1; ; number++ {
		a := chainHTTPAttempt{Attempt: number, RequestSHA256: digest}
		req, e := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(raw))
		if e != nil {
			return nil, attempts, e
		}
		req.Header.Set("Content-Type", transport.MediaType)
		sent := time.Now()
		if number == 1 {
			beforeFirstSend(sent)
		}
		a.StartedUnixNS = sent.UnixNano()
		resp, e := client.Do(req)
		if e == nil {
			a.HTTPStatus = resp.StatusCode
			b, e = io.ReadAll(io.LimitReader(resp.Body, protocol.MaxCertificateBytes))
			resp.Body.Close()
			if e == nil && resp.StatusCode != http.StatusOK {
				a.ResponseBody = string(b)
				e = fmt.Errorf("HTTP %d: %s", resp.StatusCode, b)
			}
		}
		a.DoneUnixNS = time.Now().UnixNano()
		a.ServiceMS = float64(time.Since(sent)) / 1e6
		if e == nil {
			attempts = append(attempts, a)
			return b, attempts, nil
		}
		a.Error = e.Error()
		if ctx.Err() != nil {
			attempts = append(attempts, a)
			return nil, attempts, e
		}
		pause := time.Now()
		err = budgetPause(ctx, 50*time.Millisecond)
		a.RetrySleepMS = float64(time.Since(pause)) / 1e6
		attempts = append(attempts, a)
		if err != nil {
			return nil, attempts, err
		}
	}
}
