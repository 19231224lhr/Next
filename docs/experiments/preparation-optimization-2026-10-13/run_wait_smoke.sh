#!/usr/bin/env bash
set -euo pipefail
repo=/mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree
artifacts="$repo/docs/experiments/preparation-optimization-2026-10-13/results"
root=/home/qyinf/next-preparation-optimization-20261013
taskset -c 0-3 env -u UTXO_CHAIN_COMMIT_NOTIFICATIONS -u UTXO_CHAIN_CPU_PROFILE \
  -u UTXO_CHAIN_BUILD_PROFILE GOMAXPROCS=4 \
  python3 "$repo/docs/experiments/preparation-optimization-2026-10-13/run_wait_smoke.py" \
  --bin "$repo/.scratch/preparation-after-bin" --run-dir "$root/wait-smoke" \
  > "$artifacts/wait-smoke.json"
cp "$root/wait-smoke/reports/chain-v4.json" "$artifacts/wait-smoke-native.json"
echo 'five public-wait payments completed with verified wakeups'
