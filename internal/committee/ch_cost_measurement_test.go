//go:build comet_v3

package committee

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/types"
	"utxo/crypto/chameleon"
	"utxo/protocol"
)

// Explicit opt-in measurement: no production behavior changes. Fixture creation,
// signing, key loading and CSV output are outside every timed region.
func TestCHCostMeasurement(t *testing.T) {
	out := os.Getenv("CH_COST_OUT")
	if out == "" {
		t.Skip("set CH_COST_OUT to an absolute artifact directory")
	}
	if !filepath.IsAbs(out) {
		t.Fatal("CH_COST_OUT must be absolute")
	}
	if err := os.MkdirAll(out, 0755); err != nil {
		t.Fatal(err)
	}
	const rounds, samples, warmup = 5, 1000, 100
	ticks, frequency, clockName := chCostClock(t)
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	_, _, policy, raws := cacheFixture(t, 256)
	fixtureSource := os.Getenv("CH_COST_FIXTURE")
	if fixtureSource != "" {
		b, err := os.ReadFile(fixtureSource)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(b, &raws); err != nil {
			t.Fatal(err)
		}
		if len(raws) != 256 {
			t.Fatal("expected 256 saved submissions")
		}
	}
	fixtureBytes, err := json.Marshal(raws)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(out, "fixture-submissions.json"), fixtureBytes, 0644); err != nil {
		t.Fatal(err)
	}
	if err := types.ConfigureRedaction("ch-cost-2026-10-05", policy.Key); err != nil {
		t.Fatal(err)
	}
	txs := make([]protocol.FastTx, len(raws))
	contexts := make([][]byte, len(raws))
	messages := make([][]byte, len(raws))
	for i, raw := range raws {
		p, err := protocol.DecodeDirectSubmission(raw)
		if err != nil {
			t.Fatal(err)
		}
		txs[i] = p.Tx
		if len(p.Tx.Body.Inputs) != 1 || len(p.Tx.Body.Outputs) != 1 {
			t.Fatal("expected one input and output")
		}
		if err := p.Tx.VerifyInitial(policy.Key); err != nil {
			t.Fatal(err)
		}
		contexts[i] = p.Tx.FundingContext(0, policy.Key.KeyID())
		messages[i] = p.Tx.Funding[0].ReferenceBytes()
	}
	type operation struct {
		name string
		fn   func(int)
	}
	ops := []operation{
		{"timer_empty", func(int) {}},
		{"input_commit_primitive", func(i int) {
			j := i % len(txs)
			if _, _, e := policy.Key.Commit(contexts[j], messages[j]); e != nil {
				t.Fatal(e)
			}
		}},
		{"input_verify_primitive", func(i int) {
			j := i % len(txs)
			if !policy.Key.Verify(contexts[j], messages[j], txs[j].Commitments[0], txs[j].Funding[0].Opening) {
				t.Fatal("verify")
			}
		}},
		{"input_context_and_verify", func(i int) {
			j := i % len(txs)
			tx := txs[j]
			if !policy.Key.Verify(tx.FundingContext(0, policy.Key.KeyID()), tx.Funding[0].ReferenceBytes(), tx.Commitments[0], tx.Funding[0].Opening) {
				t.Fatal("verify")
			}
		}},
		{"input_sha256_reference", func(i int) { j := i % len(txs); chCostHashSink = sha256.Sum256(messages[j]) }},
		{"input_sha256_reference_batch1000", func(i int) {
			for k := 0; k < 1000; k++ {
				chCostHashSink = sha256.Sum256(messages[(i+k)%len(txs)])
			}
		}},
	}
	type shape struct {
		Transactions, SubmissionBytes, FastTxBytes, BlockBytes, Parts, PartProtoBytes, SamePayloadOrdinaryPartProtoBytes, MetadataDeltaBytes, InputCommitmentBytes, InputOpeningBytes, InputReferenceBytes, InputVs32ByteDigestDeltaBytes int
		PartPayloadBytes                                                                                                                                                                                                                  []int
	}
	shapes := []shape{}
	for _, n := range []int{1, 32, 256} {
		bt := make([]types.Tx, n)
		for i := range bt {
			bt[i] = types.Tx(raws[i])
		}
		block := types.MakeBlock(1, bt, &types.Commit{}, nil)
		block.ChainID = "ch-cost-2026-10-05"
		block.Time = time.Unix(1700000001, 0).UTC()
		pb, err := block.ToProto()
		if err != nil {
			t.Fatal(err)
		}
		data, err := pb.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(out, fmt.Sprintf("fixture-block-%d.pb", n)), data, 0644); err != nil {
			t.Fatal(err)
		}
		ps, err := types.NewRedactablePartSet(data, types.BlockPartSizeBytes, 1, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		ordinary := types.NewPartSetFromData(data, types.BlockPartSizeBytes)
		wire := make([]*cmtproto.Part, ps.Total())
		shape := shape{Transactions: n, SubmissionBytes: len(raws[0]), BlockBytes: len(data), Parts: int(ps.Total()), InputCommitmentBytes: n * 256, InputOpeningBytes: n * 256, InputReferenceBytes: n * 33, InputVs32ByteDigestDeltaBytes: n * 480}
		ft, _ := txs[0].MarshalBinary()
		shape.FastTxBytes = len(ft)
		for i := range wire {
			wire[i], err = ps.GetPart(i).ToProto()
			if err != nil {
				t.Fatal(err)
			}
			shape.PartProtoBytes += wire[i].Size()
			op, _ := ordinary.GetPart(i).ToProto()
			shape.SamePayloadOrdinaryPartProtoBytes += op.Size()
			shape.PartPayloadBytes = append(shape.PartPayloadBytes, len(ps.GetPart(i).Bytes))
		}
		shape.MetadataDeltaBytes = shape.PartProtoBytes - shape.SamePayloadOrdinaryPartProtoBytes
		shapes = append(shapes, shape)
		suffix := fmt.Sprintf("_%dtx", n)
		ops = append(ops,
			operation{"partset_create" + suffix, func(int) {
				_, e := types.NewRedactablePartSet(data, types.BlockPartSizeBytes, 1, 0, nil)
				if e != nil {
					t.Fatal(e)
				}
			}},
			operation{"ordinary_partset_reference" + suffix, func(int) { chCostPartSink = types.NewPartSetFromData(data, types.BlockPartSizeBytes) }},
			operation{"part_digest_all" + suffix, func(int) {
				for i := 0; i < int(ps.Total()); i++ {
					p := ps.GetPart(i)
					c, e := policy.Key.Digest(types.RedactionContext(1, p.Index), p.Bytes, p.Redaction.Opening)
					if e != nil {
						t.Fatal(e)
					}
					chCostHashSink = sha256.Sum256(c[:])
				}
			}},
			operation{"part_decode_all" + suffix, func(int) {
				for _, w := range wire {
					if _, e := types.PartFromProto(w); e != nil {
						t.Fatal(e)
					}
				}
			}},
			operation{"part_add_all" + suffix, func(int) {
				recv := types.NewPartSetFromHeader(ps.Header())
				for i := 0; i < int(ps.Total()); i++ {
					if _, e := recv.AddPart(ps.GetPart(i)); e != nil {
						t.Fatal(e)
					}
				}
			}},
			operation{"original_gate_all" + suffix, func(int) {
				for i := 0; i < int(ps.Total()); i++ {
					if e := ps.GetPart(i).ValidateOriginal(1); e != nil {
						t.Fatal(e)
					}
				}
			}},
		)
	}
	// A separate direct pointer avoids charging the gate for PartSet locking.
	var opening chameleon.Opening
	opening[chameleon.Size-1] = 1
	part := &types.Part{Redaction: &types.PartRedaction{Height: 1, Opening: opening}}
	ops = append(ops, operation{"original_gate_single", func(int) {
		if e := part.ValidateOriginal(1); e != nil {
			t.Fatal(e)
		}
	}})
	ops = append(ops, operation{"original_gate_batch1000", func(int) {
		for i := 0; i < 1000; i++ {
			if e := part.ValidateOriginal(1); e != nil {
				t.Fatal(e)
			}
		}
	}})
	rawfile, err := os.Create(filepath.Join(out, "samples.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer rawfile.Close()
	w := csv.NewWriter(rawfile)
	w.Write([]string{"operation", "round", "sample", "elapsed_ns"})
	values := map[string][]int64{}
	// Rotate operation order between rounds; keep GC enabled and include pauses.
	for r := 0; r < rounds; r++ {
		for k := range ops {
			op := ops[(k+r)%len(ops)]
			for i := 0; i < warmup; i++ {
				op.fn(i)
			}
			for i := 0; i < samples; i++ {
				start := ticks()
				op.fn(i + r*samples)
				ns := (ticks() - start) * 1000000000 / frequency
				values[op.name] = append(values[op.name], ns)
				w.Write([]string{op.name, strconv.Itoa(r), strconv.Itoa(i), strconv.FormatInt(ns, 10)})
			}
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}
	type stat struct {
		Operation                         string
		Samples                           int
		MeanNS                            float64
		P50NS, P95NS, P99NS, MinNS, MaxNS int64
	}
	stats := []stat{}
	for _, op := range ops {
		v := values[op.name]
		sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
		var sum float64
		for _, x := range v {
			sum += float64(x)
		}
		q := func(p float64) int64 { return v[int(math.Ceil(p*float64(len(v))))-1] }
		stats = append(stats, stat{op.name, len(v), sum / float64(len(v)), q(.5), q(.95), q(.99), v[0], v[len(v)-1]})
	}
	report := map[string]any{"go": runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH, "gomaxprocs": 1, "num_cpu": runtime.NumCPU(), "rounds": rounds, "samples_per_round": samples, "warmup_per_operation_per_round": warmup, "input_pool": len(txs), "context_bytes": len(contexts[0]), "reference_bytes": len(messages[0]), "rsa_bits": 2048, "rsa_e": 65537, "part_size": types.BlockPartSizeBytes, "gc": "enabled default; no manual GC between samples", "percentile": "nearest-rank; individual wall-clock calls including timer overhead, no subtraction", "statistics": stats, "shapes": shapes}
	report["clock"] = clockName
	report["timer_frequency_hz"] = frequency
	report["fixture_source"] = fixtureSource
	report["gate_batch_note"] = "original_gate_batch1000 statistics are per batch of 1000; divide by 1000 for batch-average ns/op (not single-call latency quantiles)"
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(out, "summary.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
	for _, s := range stats {
		t.Logf("%s mean=%.1f ns p50=%d p95=%d n=%d", s.Operation, s.MeanNS, s.P50NS, s.P95NS, s.Samples)
	}
}

var chCostHashSink [32]byte
var chCostPartSink *types.PartSet
