//go:build comet_v3

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"utxo/crypto/chameleon"
	"utxo/internal/rules"
	"utxo/protocol"
)

func collectRepairShares(ctx context.Context, httpClient *http.Client, urls []string, path string, command any, accept func([][]chameleon.Contribution) error) error {
	if len(urls) != 4 || accept == nil {
		return protocol.ErrRule
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	raw, err := json.Marshal(command)
	if err != nil {
		return err
	}
	type result struct {
		shares []chameleon.Contribution
		err    error
	}
	responses := make(chan result, 4)
	for index, url := range urls {
		go func(index int, url string) {
			req, err := http.NewRequestWithContext(ctx, "POST", url+path, bytes.NewReader(raw))
			if err != nil {
				responses <- result{err: err}
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := httpClient.Do(req)
			if err != nil {
				responses <- result{err: err}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				responses <- result{err: rules.ErrMissing}
				return
			}
			var wire [][]byte
			err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&wire)
			var shares []chameleon.Contribution
			if err == nil {
				for _, b := range wire {
					s, e := chameleon.DecodeContribution(b)
					if e != nil || !s.ValidFor(uint(index+1)) {
						err = protocol.ErrAuth
						break
					}
					shares = append(shares, s)
				}
			}
			responses <- result{shares: shares, err: err}
		}(index, url)
	}
	var all [][]chameleon.Contribution
	last := error(protocol.ErrAuth)
	for range urls {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r := <-responses:
			if r.err != nil || len(r.shares) == 0 {
				continue
			}
			all = append(all, r.shares)
			if len(all) >= 3 {
				if last = accept(all); last == nil {
					return nil
				}
			}
		}
	}
	return last
}
