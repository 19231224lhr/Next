# FastPay runtime sensitivity

This is a separate sensitivity experiment. Preserve the official FastPay results
and label every variant with its runtime setting. The only source changes are
the runtime constructors in the official server and the receiver-confirmed
adapter. Unset `FASTPAY_RUNTIME_THREADS` calls the original `Runtime::new()`;
positive values select Tokio's existing threaded scheduler with that worker count.
Protocol operations, client timing endpoints, transport and workload stay fixed.

Prepare after active measurements finish (the script itself never builds/runs):

```powershell
python docs/experiments/fastpay-runtime-sensitivity-2026-10-14/prepare.py --source .scratch/fastpay-upstream --adapter docs/experiments/baseline-design-2026-10-13/fastpay_client.rs --destination .scratch/fastpay-runtime-sensitivity
```

The destination must not exist and must be outside the official checkout. The
script archives upstream `db39bb0cddc94b8c34dc235b1cfdae4c1285c6f2`, copies the
current official Cargo.lock, checks the frozen adapter SHA-256, and emits
`runtime-only.patch` and `runtime-provenance.json`. It never modifies the source.
The adapter example name is `baseline_client`.

Later verification/build on the measurement Linux host, using the configured
toolchain and a separate target directory (never the official target directory):

```sh
task_tools="${NEXT_BASELINE_TOOLS:-$HOME/.local/share/next-baseline-tools}"
export CARGO_HOME="$task_tools/cargo" RUSTUP_HOME="$task_tools/rustup"
export CARGO_TARGET_DIR="$task_tools/fastpay-runtime-sensitivity-target"
export PATH="$CARGO_HOME/bin:$PATH" RUSTFLAGS="--cap-lints=allow"
cd .scratch/fastpay-runtime-sensitivity
cargo +1.99.0 build --locked --release -p fastpay --bin server --example baseline_client
cargo +1.99.0 test --locked --release -p fastpay --lib --example baseline_client
sha256sum "$CARGO_TARGET_DIR/release/server" "$CARGO_TARGET_DIR/release/examples/baseline_client" Cargo.lock runtime-only.patch runtime-provenance.json
```

Inspect the emitted patch: exactly two constructors plus their identical helper
functions should change. Check the locked Tokio version remains 0.2.25 and the
lock hash matches the official build. Compare unset variant outputs/probes with
official binaries before interpreting worker-count results. Exercise `0`, invalid
text and an overflowing integer on the adapter's run path: they must fail before
workload execution. `--prepare` intentionally does not construct a runtime.

Run settings unset, 1, 2 and 4, setting `FASTPAY_RUNTIME_THREADS` identically for
all four authority server processes and the adapter. Keep CPU affinity 0–3,
fresh initial state, all other arguments and memory storage unchanged. Restart
authorities between settings; save process environment and source/binary hashes.
Alternate settings over repeated 100-payment receiver-confirmed chains and the
same mixed-load/fee-order correctness probes used for the official comparison.
Retain every failure and raw result. Compare each system's best independently
measured runtime setting alongside the untouched official baseline; tuning does
not justify modifying protocol or dropping tail, receipt or conservation checks.

Report worker counts, observed OS thread counts, chain distributions, receipt
goodput and all correctness probes. A matching worker count is a sensitivity
control, not evidence that two different runtime architectures have equal cost.

## Sequential sensitivity runner

`run_sensitivity.py` calls the unchanged official `run_fastpay.py` once per job.
It rotates official/unset/threads1/threads2/threads4 over five order positions,
continues after ordinary job failures, and preserves stdout, stderr, native raw
samples, probes, planned/actual completion, source/binary hashes, selected
environment, requested CPU affinity and sampled process affinity/thread counts.
Both authorities and adapter inherit the same runtime environment. Every job
creates fresh configuration/state. Official and unset explicitly remove any
inherited runtime override.

Example from the repository root on the measurement host:

```sh
python3 docs/experiments/fastpay-runtime-sensitivity-2026-10-14/run_sensitivity.py \
  --root /home/qyinf/next-c3-20261013/fastpay-runtime-count100 \
  --official-bin /home/qyinf/.local/share/next-baseline-tools/fastpay-target/release \
  --variant-bin /home/qyinf/.local/share/next-baseline-tools/fastpay-runtime-sensitivity-target/release \
  --official-source .scratch/fastpay-upstream \
  --variant-source .scratch/fastpay-runtime-sensitivity \
  --count 100 --repeats 5 --cpus 0-3
```

Use a new root and `--count 1000` for longer chains. Save the build command,
toolchain versions and build environment alongside the source/binary hashes.
The runner validates generated source/patch provenance against the official
source and requires identical Cargo.lock hashes before creating its output root.
`plan.json` records every intended job; `status.jsonl` and `metrics.csv` record
completed and failed attempts. A nonzero final exit means at least one job failed.

Default `/proc` sampling occurs every 200 ms outside the client timing logic.
It records observed total OS threads, including runtime support threads; it does
not equate that count with scheduler workers. Short client runs may produce no
client sample. Use `--thread-sample-ms 0` for an uninstrumented confirmation;
keep sampling settings identical across arms. Percentiles linearly interpolate
at sorted sample position `(n-1)*p`, matching comparison summaries; raw vectors
remain authoritative.
