package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"utxo/internal/member"
	"utxo/internal/state"
	"utxo/protocol"
)

func TestMeasuredChainRetryRetainsExactRequestAndFailureCost(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		time.Sleep(2 * time.Millisecond)
		if len(bodies) == 1 {
			http.Error(w, "QUORUM_UNAVAILABLE", 503)
			return
		}
		w.Write([]byte("certificate"))
	}))
	defer server.Close()
	sends := 0
	raw := []byte("exact signed request")
	response, rows, err := measuredFaultSend(context.Background(), server.Client(), server.URL, raw, func(time.Time) { sends++ })
	sum := sha256.Sum256(raw)
	if err != nil || string(response) != "certificate" || sends != 1 || len(rows) != 2 || len(bodies) != 2 || bodies[0] != bodies[1] {
		t.Fatalf("%s %+v %v", response, rows, err)
	}
	if rows[0].HTTPStatus != 503 || rows[0].ResponseBody != "QUORUM_UNAVAILABLE\n" || rows[0].ServiceMS < 2 || rows[0].RetrySleepMS < 49 || rows[1].RetrySleepMS != 0 || rows[0].RequestSHA256 != hex.EncodeToString(sum[:]) || rows[0].RequestSHA256 != rows[1].RequestSHA256 {
		t.Fatalf("lost failure timing or identity: %+v", rows)
	}
}

func TestChainWaitRequiresObservedQuorumWithoutWaitingForFourth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	gate, err := waitChainMembers(ctx, protocol.SpendFactID{1}, true, func(ctx context.Context, i int, _ protocol.SpendFactID) (member.DirectStatus, error) {
		if i == 3 {
			<-ctx.Done()
			return member.DirectStatus{}, ctx.Err()
		}
		return member.DirectStatus{Observed: true}, nil
	})
	if err != nil || len(gate.Quorum) != 3 || gate.MS >= 500 {
		t.Fatalf("waited for fourth: %+v %v", gate, err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel2()
	_, err = waitChainMembers(ctx2, protocol.SpendFactID{1}, true, func(context.Context, int, protocol.SpendFactID) (member.DirectStatus, error) {
		return member.DirectStatus{Signed: true, Height: 10}, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("signed/height substituted for observation: %v", err)
	}
}

func TestChainWaitBootstrapOnlyRequiresThreeHealthyMembers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	gate, err := waitChainMembers(ctx, protocol.SpendFactID{}, false, func(context.Context, int, protocol.SpendFactID) (member.DirectStatus, error) {
		return member.DirectStatus{}, nil
	})
	if err != nil || len(gate.Quorum) != 3 || gate.RequiresParentObservation {
		t.Fatalf("bootstrap gate: %+v %v", gate, err)
	}
}

func TestChainWaitCanceledContextCannotOpenReadyGate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := waitChainMembers(ctx, protocol.SpendFactID{1}, true, func(context.Context, int, protocol.SpendFactID) (member.DirectStatus, error) {
		return member.DirectStatus{Observed: true}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation opened gate: %v", err)
	}
}

func TestChainWaitRepollsThirdWithoutWaitingForFourth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	thirdPolls := 0
	gate, err := waitChainMembers(ctx, protocol.SpendFactID{1}, true, func(ctx context.Context, index int, _ protocol.SpendFactID) (member.DirectStatus, error) {
		if index == 3 {
			<-ctx.Done()
			return member.DirectStatus{}, ctx.Err()
		}
		if index == 2 {
			thirdPolls++
			return member.DirectStatus{Observed: thirdPolls >= 2}, nil
		}
		return member.DirectStatus{Observed: true}, nil
	})
	if err != nil || len(gate.Quorum) != 3 || thirdPolls < 2 {
		t.Fatalf("slow fourth delayed third polling: %+v %v", gate, err)
	}
}

func TestChainWaitFlagsRequireFixtureModesBeforeReadingLab(t *testing.T) {
	for _, args := range [][]string{
		{"-dir", "absent", "-wait-member-inputs"},
		{"-dir", "absent", "-wait-member-inputs", "-wait-final"},
		{"-dir", "absent", "-wait-member-inputs", "-e5-owner-fuel"},
		{"-dir", "absent", "-record-chain-attempts"},
	} {
		err := chainDirect(args)
		if err == nil || !strings.Contains(err.Error(), "requires") {
			t.Fatalf("fixture guard: %v %v", args, err)
		}
	}
}

func TestChainWaitValidatesExactPrincipalAndGenesisFee(t *testing.T) {
	org := protocol.Digest("org")
	parent := &chainHop{Tx: protocol.TxID{1}, Output: protocol.OutputID{2}, OutputData: protocol.Output{Asset: protocol.AssetCAL, Amount: 100}}
	fee := state.OriginOutput{ID: protocol.OutputID{3}, Fact: protocol.Digest("fee"), Output: protocol.Output{Asset: protocol.AssetFUEL, Amount: 1000}}
	fee.Output.Recipient.Route = protocol.Route{Kind: protocol.OrgRoute, Org: org}
	req := protocol.DirectRequest{Tx: protocol.FastTx{Body: protocol.TxBody{Network: protocol.Digest("net"), Certifier: org, Inputs: []protocol.Input{{Kind: protocol.FinalInput, Output: parent.Output}}, Fee: protocol.FeeTerms{Source: protocol.OwnerFinalUTXO, Maximum: 1000, Inputs: []protocol.Input{{Kind: protocol.FinalInput, Output: fee.ID, Evidence: fee.Fact}}}}, Claims: []protocol.InputClaim{{Output: parent.OutputData}}, Auth: []protocol.OwnerAuth{{Owner: fee.Output.Recipient.Owner}}}}
	req.Tx.Body.Inputs[0].Evidence = protocol.CreationIdentity(req.Tx.Body.Network, parent.Tx, 0, 0)
	used := map[protocol.OutputID]bool{}
	if err := validateChainWaitInputs(req, parent, []state.OriginOutput{fee}, used); err != nil {
		t.Fatal(err)
	}
	req.Tx.Body.Inputs[0].Evidence[0] ^= 1
	if err := validateChainWaitInputs(req, parent, []state.OriginOutput{fee}, used); err == nil {
		t.Fatal("wrong principal evidence accepted")
	}
	req.Tx.Body.Inputs[0].Evidence[0] ^= 1
	req.Tx.Body.Fee.Inputs[0].Evidence[0] ^= 1
	if err := validateChainWaitInputs(req, parent, []state.OriginOutput{fee}, used); err == nil {
		t.Fatal("wrong fee evidence accepted")
	}
	req.Tx.Body.Fee.Inputs[0].Evidence[0] ^= 1
	used[fee.ID] = true
	if err := validateChainWaitInputs(req, parent, []state.OriginOutput{fee}, used); err == nil {
		t.Fatal("reused genesis fee accepted")
	}
}
