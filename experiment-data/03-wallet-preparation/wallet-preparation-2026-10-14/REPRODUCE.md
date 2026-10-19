# Wallet preparation: exact-byte status cache

Baseline source: `d2a711a261e7aaa1e15794b4573d132653b9ce72` on
`fix/partial-approval-reclamation`. Apply the files under `_source/` to that
revision to reproduce this candidate. `source-sha256.json` identifies their
contents. There is no wire format, ledger rule, or fixture change.

Build `payctl` with Go 1.27.1, `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`, and
`go build -tags=comet_v3 -o payctl ./cmd/payctl`. The measured candidate hash is
`83d51a6f3705fc93583168b8e44cb6c19a080cde99c4a01694ab7c69cd5fb11f`.
That is the cache-only candidate; the final cache + commitment candidate is
`06270490ec6c5c0397fe3d88738f74862fc4467e21dccc8c673b744f9986ac23`.
The measured baseline client hash is
`0caf46a3bdec9e60b61418459f6feda066d97140b49bce7573b29b1bb37389c8`.
Go build metadata may make an independently rebuilt file's hash differ;
retain the new build provenance when rerunning.

Both arms use the identical preceding-stage member, gateway and committee
binaries. The existing compact gateway response setting stays enabled in both.
Clients intentionally differ in this experiment because the wallet is the
optimization target. This differs from earlier service-only experiments that
used one common client. Exact binaries, options, CPU affinity, execution order,
and runtime values are recorded in each series' `plan.json`.

## Commands in WSL/Ubuntu

```bash
REPO=/mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree
OLD=/home/qyinf/next-c3-speed-20261014
LAB=/home/qyinf/next-wallet-prep-20261014
SCRIPTS=$REPO/docs/experiments/c3-speed-2026-10-14
FP=/home/qyinf/.local/share/next-baseline-tools/fastpay-runtime-sensitivity-target/release

# Place the newly built client at $LAB/final-bin/payctl, then prepare once:
python3 "$SCRIPTS/prepare_variant.py" --source "$OLD/final-bin" \
  --destination "$LAB/final-compact-bin" --compact \
  --client "$LAB/final-bin/payctl"

# Use a fresh output directory for each new series.
taskset -c 0-7 python3 "$SCRIPTS/run_compare.py" \
  --root "$LAB/new-confirmation" --before "$OLD/final-compact-bin" \
  --after "$LAB/final-compact-bin" --fastpay "$FP" \
  --fastpay-runtime-threads 1 --length 1000 --repeats 7 \
  --fixture "$OLD/fixtures/chain1000" \
  --before-gomaxprocs 1 --after-gomaxprocs 1

python3 "$SCRIPTS/summarize.py" "$LAB/new-confirmation"
python3 "$SCRIPTS/timing_breakdown.py" "$LAB/new-confirmation"
```

The series names are `cache-pilot3pairs` (Next only),
`cache-confirm7triples` (cache-only confirmation including FastPay), and
`final-confirm7triples` (cache plus commitment optimization).
Initialization is excluded. Next uses four members, four committee validators,
one gateway, three rotating wallets and the existing NoSync wallet setting.
Service state is in memory; FastPay's wallet is also in memory. CPU 0–7 denotes
the shared four-core/eight-thread WSL guest budget, not exclusive host cores.
FastPay uses the previously selected one-worker runtime sensitivity build with
the upstream protocol and recipient-confirmed adapter unchanged. This is a
single-host comparison, not a WAN or durable-storage result.

The next-request metric sums `BuildMS` for payments 2 through 1000. The first
request is prepared before the chain timer in both systems. Every run retains
its original per-payment timings; `payments.csv` is the flattened export.
End-to-end timing stops when the last recipient can continue paying. Next's
background public/member closure remains enabled and must complete afterward.

Run diagnostic profiles separately, after formal timing has stopped:

```bash
taskset -c 0-7 python3 "$SCRIPTS/profile_chain.py" \
  --root "$LAB/new-diagnostic" --bin "$LAB/final-compact-bin" \
  --client "$LAB/final-bin/payctl" \
  --fixture "$OLD/fixtures/chain1000" --gomaxprocs 1
```

Validation commands: `go test -tags=comet_v3 -count=1 ./...` and
`go test -race -tags=comet_v3 -count=1 ./internal/wallet`.
Do not run builds, tests, other performance jobs or profiling during formal
timing. The raw laboratories stay on WSL ext4; exported evidence excludes
fixture private keys and live wallet databases.
