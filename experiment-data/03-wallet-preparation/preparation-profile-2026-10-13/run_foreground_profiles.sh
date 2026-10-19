#!/usr/bin/env bash
set -euo pipefail
repo=/mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree
artifacts="$repo/docs/experiments/preparation-profile-2026-10-13/results"
root=/home/qyinf/next-preparation-observer-ab-20261013
for arm in polling events; do
  value=0
  if [[ "$arm" == events ]]; then value=1; fi
  taskset -c 0-3 env GOMAXPROCS=4 UTXO_CHAIN_BUILD_PROFILE=1 \
    UTXO_CHAIN_COMMIT_NOTIFICATIONS="$value" \
    UTXO_CHAIN_CPU_FOREGROUND_ONLY=1 UTXO_CHAIN_CPU_PROFILE="$root/foreground-$arm.cpu.pprof" \
    python3 "$repo/docs/experiments/baseline-design-2026-10-13/run_next.py" \
    --bin "$repo/.scratch/preparation-profile-bin" --run-dir "$root/foreground-cpu-$arm" \
    --workload chain --length 100 --count 100 --storage memory \
    > "$artifacts/foreground-cpu-$arm.json"
  cp "$root/foreground-cpu-$arm/reports/chain-v4.json" "$artifacts/foreground-cpu-$arm-native.json"
  cp "$root/foreground-$arm.cpu.pprof" "$artifacts/foreground-$arm.cpu.pprof"
  echo "foreground CPU sample complete: $arm"
done
