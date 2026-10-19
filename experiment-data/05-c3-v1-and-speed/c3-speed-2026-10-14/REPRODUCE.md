# Reproducing the C3 speed comparisons

Run services in Ubuntu/WSL on Linux ext4. Do not place live databases under `/mnt/c`. Use a new output directory for every trial series. The runners reject existing run directories and retain failures.

Recorded environment: AMD Ryzen AI 9 HX 370, Go 1.27.1, Ubuntu/WSL kernel
6.6.87.2. Core/SMT labels below describe the guest topology. CPU affinity limits
these guest processes; it does not establish exclusive host physical cores or
isolation from other host activity.

## Measurement contract

- All Next service binaries include the new issuer-funded C3 protocol. The frozen C3 service build comes from `a9d943e`; delivery HEAD before this optimization was `7e98a27`.
- Chain/mixed Next runs four members, one gateway and four committee validators. The separate two-organization fee-order regression runs eight members and two gateways. FastPay runs four authorities and its native client adapter. Both systems share the same process CPU affinity budget. Initialization is excluded from timed work.
- The chain cycles 100 units among three wallets. A recipient uses the preceding payment to fund the next; it is not independently prefunded. The endpoint is receipt acceptance enabling continuation. Public/member closure remains enabled and is recorded separately for Next.
- FastPay uses the pinned upstream revision and the unchanged `receiver-confirmed` adapter. It checks funding, obtains a real certificate, invokes the official recipient API, verifies balances, and performs an additional continuation probe after timing.
- Service state is in memory for both systems. Next wallet writes use the existing NoSync workload configuration; FastPay wallet state is in memory. This is not a durable-storage or WAN benchmark.
- CPU `0-3` exposes two cores/four SMT threads in this WSL machine; CPU `0-7` exposes four cores/eight SMT threads. Next `GOMAXPROCS` and FastPay Tokio worker counts are recorded independently. They are runtime controls, not identical scheduling models.
- Formal comparisons contain no CPU profile or settlement tracing. Diagnostic runs are separate. Avoid concurrent builds, tests, benchmarks and profiles.

## Existing local paths

```text
Repository: C:/Users/Bocchi/Desktop/Next/.e8-worktree
Linux experiments: /home/qyinf/next-c3-speed-20261014
Frozen C3: /home/qyinf/next-c3-20261013/current-bin
Official FastPay: /home/qyinf/.local/share/next-baseline-tools/fastpay-target/release
Runtime-only FastPay: /home/qyinf/.local/share/next-baseline-tools/fastpay-runtime-sensitivity-target/release
```

`variant.json` in each Next wrapper directory records actual binaries, their SHA-256 hashes and extra CLI options. `plan.json` records wrapper hashes, workload arguments, CPU affinity, runtime settings and alternating execution order. Each run retains its manifest and native report.

The non-E5 workload client now accepts chain lengths up to 1000. The E5 limit and default lengths remain unchanged. Both frozen and optimized services use the same common client for the long-chain comparisons; see `long-chain-plan.md`.

## Running a comparison

In WSL, replace `AFTER` with the chosen recorded wrapper directory. `BEFORE` must be the matching common-client frozen or preceding-stage directory. Use the exact values in the desired series' `plan.json` to reproduce a particular result.

The selected Stage 9b has now passed final full/race tests and a clean Linux
rebuild. The example below uses `final-compact-bin` and the matching common
client in `frozen-final-client-bin`; `final-bin` retains the new raw binaries.
The earlier completed Stage 9b series used `stage9b-long-bin`. New payctl,
gateway and committee hashes differ from that earlier group, while member is
identical. Earlier Stage 9b series shared a pre-9b client; the fresh group shares
the same newly built client across both Next arms. Independent nine-trial
`final-build-chain100-9triples` and seven-trial
`final-build-chain1000-7triples` are complete.
Earlier results must not be silently assigned to these new artifacts,
or interpreted as a rebuild-only performance experiment.
Use the original recorded wrappers if reproducing a historical series.

```bash
REPO=/mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree
LAB=/home/qyinf/next-c3-speed-20261014
BEFORE=$LAB/frozen-final-client-bin
AFTER=$LAB/final-compact-bin
FASTPAY=/home/qyinf/.local/share/next-baseline-tools/fastpay-runtime-sensitivity-target/release

taskset -c 0-3 python3 "$REPO/docs/experiments/c3-speed-2026-10-14/run_compare.py" \
  --root "$LAB/my-new-chain-series" --before "$BEFORE" --after "$AFTER" \
  --fastpay "$FASTPAY" --fastpay-runtime-threads 1 \
  --repeats 7 --length 1000 --fixture "$LAB/fixtures/chain1000" \
  --before-gomaxprocs 1 --after-gomaxprocs 1

python3 "$REPO/docs/experiments/c3-speed-2026-10-14/summarize.py" \
  "$LAB/my-new-chain-series"
python3 "$REPO/docs/experiments/c3-speed-2026-10-14/analyze_chain_bands.py" \
  "$LAB/my-new-chain-series"
```

For official FastPay, use its official directory and omit `--fastpay-runtime-threads`. For a 100-payment series use `--length 100`. For a new machine, omit `--fixture` to let the runner prepare an identical pre-service fixture for each Next pair. Test fixture signing keys are intentionally excluded from the exported evidence bundle.

CPU 0–7 is a separate four-physical-core/eight-SMT-thread sensitivity budget.
Its 15-run FastPay runtime selection pilot chose worker=1; its three-pair
same-code Next GOMAX1→4 pilot retained GOMAX1. The independent seven-triple
`final-eightcpu-chain1000-7triples` confirmation is complete. To reproduce
that configuration, use taskset 0–7 with the selected runtime values and the
exact plan, and never merge it with CPU 0–3 summaries. The three-round selection
pilots are not independent cross-system performance confirmation themselves.

For mixed load, use `--suite mixed --only-next --rate 100 --duration 30` and omit the chain fixture. The driver deliberately skips missed pacing slots instead of producing catch-up bursts. Report nominal scheduled opportunities, actual submissions, receipt latency and closure separately; this load is not a saturation-throughput measurement.

A fixed mixed-workload pre-service fixture may be reused when the runner's
recorded fixture parameters match. Cloning must still create fresh run state;
never reuse a started laboratory or substitute the chain fixture. Initialization
is outside timing, so this does not authorize suppressing timed service work.

The completed final mixed series is `final-build-mixed5pairs` under CPU 0–3:
all ten runs complete, actual submissions all received and closed; each of five
pairs improves P50/P95. Missed pacing slots remain visible, so successful actual
completion is not equivalent to processing all 3000 nominal opportunities.

## C3 functional regression on live services

`run_matrix.py --suite orders --repeats 3` runs source-first, child-first and compensated-then-repaid flows using the same signed payments within each repetition. Supply the final Next directory as `--current-bin` and any valid directory as the unused `--baseline-bin` for this suite. These controlled economic scenarios suppress automatic source recovery to impose the intended ordering; chain/mixed performance runs do not suppress recovery.

The final fee verifier's revised replay policy accepts only the explicitly
recorded HTTP rejection with unchanged complete escrow/pool snapshots. The nine
final observations returned HTTP 503 / `http-rejected`; nonempty ReplayError
alone is not an economic failure under the existing producer semantics. The v2
PASS is an observation check, not ledger-inclusion or execution-idempotence
proof. Keep the original false-failure output separately.

```bash
taskset -c 0-3 env GOMAXPROCS=1 python3 \
  "$REPO/docs/experiments/c3-issuer-funded-2026-10-13/run_matrix.py" \
  --root "$LAB/my-new-fee-regression" --suite orders --repeats 3 \
  --baseline-bin "$BEFORE" --current-bin "$AFTER"

python3 "$REPO/docs/experiments/c3-speed-2026-10-14/validate_fee_regression.py" \
  --root "$LAB/my-new-fee-regression" --out "$LAB/my-new-fee-regression-csv" \
  --repeats 3
```

## Build and test

Use the repository's Go toolchain and `comet_v3` tag. Build `payctl`, `member`, `gateway` and `committee`; run `go test -tags=comet_v3 -count=1 ./...` and the affected race suites. This round's optimized Linux binaries were cross-built from Windows using `GOOS=linux`, `GOARCH=amd64`, then copied to ext4. Do not replace frozen binaries. Keep final build logs, source hashes and binary hashes with `variant.json`; source equivalence alone is not a substitute for identifying the binaries used in a result.

`final-verification/status.json` records successful full tests, tagged affected
race tests and all four Linux builds. `final-verification/manifest.json` records
`all_steps_passed=true`, changed source SHA-256 values and the raw binary hashes.
This verification passes before the separately recorded fresh-binary timing confirmations;
it is not a performance confirmation by itself.

FastPay's official source, lock file and adapter hashes are recorded under `../fastpay-runtime-sensitivity-2026-10-14/`. Its optional worker-count version changes only the two Tokio runtime constructors. An unset worker count retains the original constructor; invalid counts are rejected. The stock binary remains available separately.

## Evidence interpretation

`runs.csv` contains individual-run metrics. `summary.json` summarizes those run metrics, rather than pooling all payments across repetitions. Keep ranges and paired results. Report intermediate regressions and tuned/stock results separately. A CPU reduction, lower receipt P50, or one winning pilot does not by itself establish a shorter end-to-end chain.

## Delivered code and evidence

Windows evidence is available under `raw-results/` next to this document.
The export records 6482 files and 1,085,792,052 bytes; its manifest is the source
of file-level hashes and integrity checks.
`final-verification/export-integrity.json` records all 6482 file hashes correct,
no missing files and no source changes during export. `final-results.csv` is the
three-row formal comparison table generated directly from retained summaries.
`export-manifest.json` lists the exported paths, file hashes and metadata-based
dataset status. It preserves exploratory/pilot/negative runs, the original fee
checker false failures and the revised check; keys, service configuration,
databases, genesis snapshots, binaries and large binary profiles are excluded.

`final-code-delivery/` is the separate code-delivery entry point. Check
`source-manifest.json` for `base_head`, changed/new Go files and per-file hashes;
check `package-manifest.json` for `final-code.patch` and
`final-code-source.zip` hashes. `reverse-check.log` records the check against
the delivered checkout; it is not permission to apply changes blindly to an
unrelated branch. On a matching clean base, inspect the patch and run
`git apply --check final-code.patch` before applying it. The patch/manifest
separate code from unrelated manuscript edits and experiment secrets.

Final long-chain timing, background closure and band analyses were derived after
timed jobs ended. All seven analysis commands exit 0 in
`raw-results/final-analysis-status.json`. Closure checks cover retained wallet
public observations and recorded all-member closure, not a new exhaustive
store-key or economic audit. Per-run timing residuals are below 0.01 ms;
independent column medians need not add to median chain totals.

`final-verification/source-integrity-final.json` confirms all 23 changed Go
source hashes still match the final verification/build record.
