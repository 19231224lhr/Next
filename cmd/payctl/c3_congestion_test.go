package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"utxo/internal/rules"
	"utxo/protocol"
)

func TestC3CongestionHTTPRecordsActualAttemptAndRejection(t *testing.T) {
	for _, status := range []int{http.StatusAccepted, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if string(body) != "signed-raw" || r.URL.Path != "/v1/commands" {
					t.Error("changed public bytes or endpoint")
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("actual response"))
			}))
			defer server.Close()
			a, e := c3CongestionHTTP(context.Background(), server.Client(), server.URL, []byte("signed-raw"), time.Now())
			if a.HTTPStatus != status || a.ResponseBody != "actual response" || a.EndOffsetNS < a.StartOffsetNS || (e == nil) != (status == http.StatusAccepted) {
				t.Fatalf("attempt %+v %v", a, e)
			}
		})
	}
}

func TestC3CongestionPriorityUsesSignedPublishedTier(t *testing.T) {
	_, _, n, f := c3CLIFixture(t)
	n.Direct.C3 = nil
	n.Genesis.Grants = f.Genesis.Grants
	n.Accounts = n.Accounts[:2]
	if e := configureFinalC3Lab(&n, 2); e != nil {
		t.Fatal(e)
	}
	p, e := n.Direct.Policy(n.Schedule, n.Organizations)
	if e != nil {
		t.Fatal(e)
	}
	o := f.Genesis.Outputs[0]
	base, e := budgetRequest(n, p, f.Org, f.Owner, protocol.Input{Kind: protocol.FinalInput, Output: o.ID, Evidence: o.Fact}, o.Output, o.Output.Recipient, nil)
	if e != nil {
		t.Fatal(e)
	}
	originalID := base.Tx.ID()
	paid, e := c3CongestionPriority(base, n, p, f.Owner)
	if e != nil {
		t.Fatal(e)
	}
	if base.Tx.ID() != originalID || paid.Tx.ID() == originalID {
		t.Fatal("priority builder mutated base identity")
	}
	c := paid.Tx.Body.Fee.C3
	if c.PriorityFee != 10 || c.PriorityReward != 2 || c.Quote != 12 || paid.Tx.Body.Outputs[0].Amount+12 != o.Output.Amount || len(paid.Tx.Body.Fee.Inputs) != 0 {
		t.Fatalf("invalid signed tier %+v", c)
	}
	if paid.Tx.VerifyAuth() != nil {
		t.Fatal("priority owner signature")
	}
	if _, e = rules.PrepareDirectVector(paid.Tx, p); e != nil {
		t.Fatal(e)
	}
}
