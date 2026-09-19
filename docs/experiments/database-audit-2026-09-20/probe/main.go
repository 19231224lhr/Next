// Diagnostic only: existing experiment DBs are opened read-only. Writes use new files.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
	"utxo/internal/state"
	"utxo/protocol"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func size(path string) int64 { info, err := os.Stat(path); must(err); return info.Size() }

func main() {
	if len(os.Args) != 3 {
		panic("usage: probe retained-lab new-output-parent")
	}
	lab, parent := os.Args[1], os.Args[2]
	out, err := os.MkdirTemp(parent, "db-audit-probe-")
	must(err)
	result := map[string]any{"scratch": out, "sync_enabled": true}
	var inspections []any
	var submissions [][]byte
	for _, name := range []string{"committee0/committee.db", "org0-member0/member.db", "org1-member0/member.db", "gateway0/gateway.db", "gateway1/gateway.db", "bench-v4-0.db"} {
		path := filepath.Join(lab, name)
		db, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
		must(err)
		groups := map[byte][2]int{}
		var logical int64
		must(db.View(func(tx *bolt.Tx) error {
			logical = tx.Size()
			return tx.Bucket([]byte("state.v2")).ForEach(func(k, v []byte) error {
				kind := byte(255)
				if len(k) >= 3 && k[0] == 0 && k[1] == 2 {
					kind = k[2]
				}
				g := groups[kind]
				g[0]++
				g[1] += len(k) + len(v)
				groups[kind] = g
				if name == "gateway0/gateway.db" && bytes.HasPrefix(k, state.Key(state.KeyCollected)) {
					payment, err := protocol.DecodeDirectPayment(v)
					if err != nil {
						return err
					}
					raw, err := payment.Submission().MarshalBinary()
					if err != nil {
						return err
					}
					submissions = append(submissions, raw)
				}
				return nil
			})
		}))
		must(db.Close())
		inspections = append(inspections, map[string]any{"file": name, "file_bytes": size(path), "logical_bytes": logical, "key_prefix_count_bytes": groups})
	}
	result["inspections"] = inspections
	if len(submissions) != 100 {
		panic(fmt.Sprintf("expected 100 archived submissions, got %d", len(submissions)))
	}
	var decodeMS []float64
	for repeat := 0; repeat < 5; repeat++ {
		start := time.Now()
		for _, raw := range submissions {
			_, err := protocol.DecodeDirectSubmission(raw)
			must(err)
		}
		decodeMS = append(decodeMS, float64(time.Since(start).Nanoseconds())/1e6)
	}
	result["decode_100_ms"] = decodeMS
	var rounds []any
	var expected []byte
	for round, premap := range []bool{false, true, true, false, false, true} {
		path := filepath.Join(out, fmt.Sprintf("probe-%d.db", round))
		options := &bolt.Options{Timeout: time.Second}
		if premap {
			options.InitialMmapSize = 16 << 20
		}
		start := time.Now()
		db, err := bolt.Open(path, 0600, options)
		must(err)
		must(db.Update(func(tx *bolt.Tx) error { _, err := tx.CreateBucket([]byte("data")); return err }))
		setup := time.Since(start)
		var rows []any
		id := 0
		value := bytes.Repeat([]byte{0x31}, 1024)
		for _, count := range []int{1, 63, 1, 35} {
			before := db.Stats()
			oldSize := size(path)
			started := time.Now()
			must(db.Update(func(tx *bolt.Tx) error {
				b := tx.Bucket([]byte("data"))
				for n := 0; n < count*7; n++ {
					key := sha256.Sum256([]byte(fmt.Sprint(id)))
					id++
					if err := b.Put(key[:], value); err != nil {
						return err
					}
				}
				return nil
			}))
			elapsed := time.Since(started)
			after := db.Stats()
			diff := after.Sub(&before)
			rows = append(rows, map[string]any{"payments": count, "keys": count * 7, "elapsed_ms": float64(elapsed.Nanoseconds()) / 1e6, "native_write_ms": float64(diff.TxStats.GetWriteTime().Nanoseconds()) / 1e6, "spill_ms": float64(diff.TxStats.GetSpillTime().Nanoseconds()) / 1e6, "file_before": oldSize, "file_after": size(path), "write_count": diff.TxStats.GetWrite()})
		}
		h := sha256.New()
		must(db.View(func(tx *bolt.Tx) error {
			return tx.Bucket([]byte("data")).ForEach(func(k, v []byte) error { h.Write(k); h.Write(v); return nil })
		}))
		if expected == nil {
			expected = h.Sum(nil)
		} else if !bytes.Equal(expected, h.Sum(nil)) {
			panic("state mismatch")
		}
		must(db.Close())
		rounds = append(rounds, map[string]any{"premap": premap, "setup_ms": float64(setup.Nanoseconds()) / 1e6, "batches": rows})
	}
	result["synthetic_rounds"] = rounds
	result["all_synthetic_states_equal"] = true
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	must(e.Encode(result))
}
