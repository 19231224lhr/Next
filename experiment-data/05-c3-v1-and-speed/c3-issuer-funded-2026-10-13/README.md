# C3 issuer-funded implementation and experiments

Implementation and real-service evaluation bundle. Measured results are generated from native reports; design examples are never substituted for measurements.

Authoritative design: ../../superpowers/specs/2026-10-13-c3-issuer-funded-design.md.
Plan: ../../superpowers/plans/2026-10-13-c3-issuer-funded.md.

## Baseline

Source commit `909ac2c`, branch `fix/partial-approval-reclamation`.
The code-only source was extracted with `git archive` into `.scratch/c3-baseline-source`; no working manuscript changes were copied. Linux binaries were freshly cross-compiled with Go 1.27.1, `GOOS=linux GOARCH=amd64`, `-tags=comet_v3` for payctl/member/gateway/committee. CometBFT dependency is the existing v0.38.26 overlay selected by go.mod. Binary hashes: baseline-binaries.json.

The baseline is the request-byte reuse and commit-notification version, not the earlier slower request-preparation version.

Before C3 source changes, this command passed:

```
go test -tags=comet_v3 ./protocol ./internal/rules ./internal/member ./internal/wallet ./internal/committee ./cmd/payctl
```

Observed package outputs: protocol0.818s, rules3.002s, member2.494s, wallet1.434s, committee2.047s, payctl2.509s. These are test-run durations, not protocol performance data.

Host: Windows with Ubuntu WSL2 Linux6.6.87.2, Go1.27.1 windows/amd64 cross-compiler, Python3.12.3 Linux. WSL exposes24logicalCPUs and15544MiB memory. Timed runs shared CPUs0-3 and GOMAXPROCS4 and used Linux ext4 data directories. Exact per-run configuration and storage mode are retained in the native evidence archive.

## Completed results

See [RESULTS.zh-CN.md](RESULTS.zh-CN.md) for all three experiments, and [IMPLEMENTATION.zh-CN.md](IMPLEMENTATION.zh-CN.md) for code/test/proof mapping. The measured C3 Go source is `a9d943e6ae04b458069cd46806043c4142a03a6d`; the later delivery commit contains scripts, CSVs and documentation only. All 53 formal native runs completed successfully. `csv/evidence-check.json` checks the matrix, identical signed order-comparison transactions, fixed observed user fees and issuer pool reconciliation.

## Measurement boundaries

- Fee order/delay experiments explicitly control source submission. Existing source-recovery disabling is permitted only as a declared experiment control; public validation, actual signatures, consensus, compensation decisions, and repayment remain real.
- Normal performance comparisons retain automatic recovery and background execution in both arms.
- User fixed fees are collected only at successful own execution. Before that, an issuer may already have paid processing costs without receiving revenue.
- Report paid rewards separately from burned charges and released capital. A budget reservation is not an additional asset.
- Preserve raw failed attempts and retry counts; successful-only cleanup/replacement is not allowed.

## Reproduce the service runs

Run from Linux/WSL. The repository's existing CometBFT overlay must be available at the `go.mod` replacement path. Build `payctl`, `member`, `gateway`, and `committee` using `go build -tags=comet_v3 -o <bin>/<command> ./cmd/<command>`. Build the baseline from `909ac2c` in a separate checkout/source export, using the same Go version and build flags. Do not change the production checkout to the baseline during a run.

The exact baseline build and hashes are in `baseline-build.json` and `baseline-binaries.json`; each new run records all four executable hashes, environment, CPU affinity and generated configuration in `manifest.json`. Use the current C3 implementation commit recorded in the final report. Preparation happens before timing.

Example (adjust absolute paths to the local checkout and freshly built binaries):

```bash
export GOMAXPROCS=4
S=/mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree/docs/experiments/c3-issuer-funded-2026-10-13
R=/home/qyinf/next-c3-20261013/measured
B=/home/qyinf/next-c3-20261013/baseline-bin
N=/home/qyinf/next-c3-20261013/current-bin
taskset -c 0-3 python3 "$S/run_matrix.py" --root "$R/orders" --baseline-bin "$B" --current-bin "$N" --suite orders --repeats 3
taskset -c 0-3 python3 "$S/run_matrix.py" --root "$R/delays" --baseline-bin "$B" --current-bin "$N" --suite delays --repeats 3 --delays 0,100,500,1500,4000
taskset -c 0-3 python3 "$S/run_delay_extension.py" --root "$R/delays-extra" --fixtures "$R/delays" --current-bin "$N"
taskset -c 0-3 python3 "$S/run_matrix.py" --root "$R/chain" --baseline-bin "$B" --current-bin "$N" --suite chain --repeats 5
taskset -c 0-3 python3 "$S/run_matrix.py" --root "$R/mixed" --baseline-bin "$B" --current-bin "$N" --suite mixed --repeats 5 --rate 100 --duration 30
taskset -c 0-3 python3 "$S/run_matrix.py" --root "$R/diagnostic" --baseline-bin "$B" --current-bin "$N" --suite diagnostic --repeats 3
python3 "$S/analyze.py" --root "$R" --out "$R/csv"
python3 "$S/check_evidence.py" --csv "$R/csv"
python3 "$S/report_results.py" --csv "$R/csv" --out "$S/RESULTS.zh-CN.md"
# Python with Matplotlib is needed only for the final figures:
python3 "$S/plot_results.py" --csv "$R/csv" --out "$S/figures"
```

Run these commands sequentially. A suite refuses an existing plan/output directory; failed attempts are retained. `run_c3.py --help` also supports one fresh run. `python3 -m unittest test_metrics.py` tests the reducers from this directory.

The 12 s delay extension was added after the original 0–4000 ms conditions produced no compensation. It reuses each repetition's prepared inputs in fresh service state; all original conditions remain included. This tests the longer-delay transition, rather than replacing observations with a preferred outcome.

## How to read the outputs

- `fee_stages.csv`: per-certificate escrow snapshots, including the intermediate compensated-but-not-executed state.
- `fee_pairs.csv`: actual user fees, signed transaction identities, observed order, repeat-submission and history-repair observations.
- `fee_runs.csv`: pool income/expenses and completed-pair totals; incomplete reconciliation is explicit.
- `budget_samples.csv`: 50 ms nominal polling with actual offsets/read costs retained in JSON. Peaks are sampled lower bounds, and cross-pool queries are sequential. Member reserved counters include retained spent exposure; they are not an additional FUEL asset.
- `performance_runs.csv`, `chain_hops.csv`, `mixed_payments.csv`: per-run and per-payment observations. Native failures remain recorded.
- `diagnostic_spans.csv`: same-node member monotonic spans and committed committee execution wall-clock spans. These measure the complete atomic evaluation/execution path, not an isolated FUEL instruction or HTTP round trip.
- `summary.csv`: median and min/max of run-level statistics. P50/P95 use linear interpolation. Runs, rather than individual payments, are the repeated experimental units.

Mixed workload warmup is a time window. Formal payments are selected by native `ScheduledNS` within `[FormalNS, EndSendNS)`. Report attempted-payment completion and nominal offered-load completion separately; scheduler skip counters include warmup. In-window throughput counts only the formal cohort completing inside that window. After-drain completion is a separate result.

The order experiment reuses a prepared fixture within each repetition. Performance and diagnostic runs use alternating baseline/C3 and C3/baseline order. Main performance measurements have request tracing disabled; diagnostic runs enable the same existing tracing in both versions.

## Raw evidence

`native-evidence.tar.gz` (about 57 MiB) and `smoke-evidence.tar.gz` are local deliverables, excluded from Git; their adjacent JSON indexes record SHA-256 and scope. The formal archive contains 4,994 native/config/log/fixture files. It includes experiment-only disposable signing keys so fixture inputs can be reproduced. No live service databases are needed to interpret the signed requests, verified blocks, execution results, snapshots and per-run CSVs. Original WSL run directories remain at `/home/qyinf/next-c3-20261013/measured`.

To regenerate an archive into a **new** output filename:

```bash
python3 "$S/package_evidence.py" --root "$R" --out /path/to/new-native-evidence.tar.gz
```

All smoke and harness-development failures are preserved separately in the smoke archive. They are excluded from formal measurements, rather than substituted for formal observations. No experiment processes remain running after the runners return.
