package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// This observes committed application state, not a cryptographic proof. The
// caller subsequently verifies the output proof and records its block height.
func observeCommit(ctx context.Context, client *http.Client, url string) error {
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(req)
		if err != nil {
			return err
		}
		var outcome struct{ PublicPhase string }
		if response.StatusCode == http.StatusOK {
			err = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&outcome)
		} else if response.StatusCode != http.StatusNotFound {
			err = fmt.Errorf("committee observation HTTP %d", response.StatusCode)
		}
		response.Body.Close()
		if err != nil {
			return err
		}
		if outcome.PublicPhase == "SETTLED" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
