//go:build comet_v3

package main

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	tss "github.com/cloudflare/circl/tss/rsa"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
	"utxo/crypto/chameleon"
)

func TestRepairHTTPHonestQuorum(t *testing.T) {
	raw, err := os.ReadFile("../../crypto/chameleon/testdata/rsa2048.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := chameleon.NewPublic(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := tss.Deal(rand.Reader, 4, 3, key, true)
	if err != nil {
		t.Fatal(err)
	}
	ctxBytes, old, next := []byte("repair-http"), []byte("original"), []byte("reserve")
	commitment, opening, err := pub.Commit(ctxBytes, old)
	if err != nil {
		t.Fatal(err)
	}
	wire := make([][]byte, 4)
	var wrong []byte
	for i, k := range keys {
		s, err := chameleon.NewSigner(pub, k)
		if err != nil {
			t.Fatal(err)
		}
		part, err := s.Adapt(ctxBytes, old, next, commitment, opening)
		if err != nil {
			t.Fatal(err)
		}
		wire[i], err = part.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			bad, err := s.Adapt(ctxBytes, old, []byte("wrong target"), commitment, opening)
			if err != nil {
				t.Fatal(err)
			}
			wrong, err = bad.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, mode := range []string{"empty", "duplicate-index", "wrong-value-first", "silent", "only-two"} {
		t.Run(mode, func(t *testing.T) {
			var urls []string
			for i := range 4 {
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					if mode == "silent" && i == 0 {
						select {
						case <-r.Context().Done():
						case <-time.After(3 * time.Second):
						}
						return
					}
					if mode == "only-two" && i < 2 {
						http.Error(w, "unavailable", 503)
						return
					}
					if i == 3 {
						time.Sleep(20 * time.Millisecond)
					} // bad response arrives before the third honest one
					row := [][]byte{wire[i]}
					if i == 0 {
						switch mode {
						case "empty":
							row = nil
						case "duplicate-index":
							row = [][]byte{wire[1]}
						case "wrong-value-first":
							row = [][]byte{wrong}
						}
					}
					_ = json.NewEncoder(w).Encode(row)
				}))
				t.Cleanup(s.Close)
				urls = append(urls, s.URL)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			verified := false
			err := collectRepairShares(ctx, &http.Client{Timeout: 2 * time.Second}, urls, "/shares", struct{}{}, func(rows [][]chameleon.Contribution) error {
				if mode == "duplicate-index" {
					for _, row := range rows {
						for _, part := range row {
							if part.ValidFor(1) {
								t.Error("spoofing response reached callback")
							}
						}
					}
					if len(rows) != 3 {
						t.Error("unfiltered impersonating row reached callback")
					}
				}
				var shares []chameleon.Contribution
				for _, row := range rows {
					if len(row) == 1 {
						shares = append(shares, row[0])
					}
				}
				_, err := pub.Combine(ctxBytes, old, next, commitment, opening, shares)
				verified = err == nil
				return err
			})
			if mode == "only-two" {
				if err == nil || verified {
					t.Fatal("accepted two honest responses")
				}
			} else if err != nil || !verified || ctx.Err() != nil {
				t.Fatalf("honest quorum blocked: err=%v verified=%v deadline=%v", err, verified, ctx.Err())
			}
		})
	}
}
