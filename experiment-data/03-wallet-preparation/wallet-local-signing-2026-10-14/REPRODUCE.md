# Reproduction

Apply the 15 files in `_source/` to `d2a711a261e7aaa1e15794b4573d132653b9ce72`. This includes the preceding wallet status-cache and GCD optimization. The before arm is the preceding accepted client, not unmodified d2a711a. Its sources are in `../wallet-preparation-2026-10-14/_source/`.

Go 1.27.1, GOOS=linux GOARCH=amd64 CGO_ENABLED=0:

```sh
go build -tags=comet_v3 -o payctl ./cmd/payctl
chmod +x payctl
```

Measured before SHA256: `06270490ec6c5c0397fe3d88738f74862fc4467e21dccc8c673b744f9986ac23`.
Local signer only: `4005c8f2e3cf3d588a1b97850c35ba6ae64d19bd13035ae0acd16ae5507db0bf`.
Both optimizations: `d89a7051cc852c3445f307a6c213f43d671388258198c4b0667ba33b259c64d1`.
All service hashes, ordering, affinity and runtime settings are in each `results/*/plan.json`.

Existing WSL laboratory: `/home/qyinf/next-wallet-local-20261014`. Use a fresh root for every run:

```sh
REPO=/mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree
SCRIPTS=$REPO/docs/experiments/c3-speed-2026-10-14
OLD=/home/qyinf/next-c3-speed-20261014
LAB=/home/qyinf/next-wallet-local-20261014
FP=/home/qyinf/.local/share/next-baseline-tools/fastpay-runtime-sensitivity-target/release

python3 "$SCRIPTS/prepare_variant.py" --source "$OLD/final-bin" \
 --destination "$LAB/new-compact-bin" --compact --client /absolute/path/to/new/payctl
taskset -c 0-7 python3 "$SCRIPTS/run_compare.py" \
 --root "$LAB/new-confirmation" \
 --before /home/qyinf/next-wallet-prep-20261014/final-compact-bin \
 --after "$LAB/new-compact-bin" --fastpay "$FP" --fastpay-runtime-threads 1 \
 --length 1000 --repeats 7 --fixture "$OLD/fixtures/chain1000" \
 --before-gomaxprocs 1 --after-gomaxprocs 1
python3 "$SCRIPTS/summarize.py" "$LAB/new-confirmation"
python3 "$SCRIPTS/timing_breakdown.py" "$LAB/new-confirmation"
```

`next_request_build_ms` sums payments 2–1000, excluding the first pre-timer request consistently in both systems. Final receipt is accepted and saved by its recipient. Public/member closure is tracked separately. Setup and initialization are excluded. No profiler, concurrent builds or tests during comparisons.

`sign-pilot3pairs-rerun`: preceding client versus signer-only, 3 pairs.
`cache-pilot3pairs`: signer-only versus both, 3 pairs.
`final-confirm7triples`: preceding client versus both versus FastPay, 7 triples.
The export script accepts the lab root and saves only evidence, excluding fixture keys and live databases.

Full tests: `go test -tags=comet_v3 -count=1 -timeout=180s ./...`.
Race: `go test -race -tags=comet_v3 -count=1 ./internal/wallet ./protocol`.
