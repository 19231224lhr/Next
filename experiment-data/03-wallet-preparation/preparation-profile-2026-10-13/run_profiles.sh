#!/usr/bin/env bash
set -euo pipefail
repo=/mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree
artifacts="$repo/docs/experiments/preparation-profile-2026-10-13"
root=/home/qyinf/next-preparation-profile-20261013
mkdir -p "$artifacts/results" "$root"
for i in 0 1 2 3 4; do
  taskset -c 0-3 env GOMAXPROCS=4 UTXO_CHAIN_BUILD_PROFILE=1 \
    python3 "$repo/docs/experiments/baseline-design-2026-10-13/run_next.py" \
    --bin "$repo/.scratch/preparation-profile-bin" --run-dir "$root/timing-$i" \
    --workload chain --length 100 --count 100 --storage memory \
    > "$artifacts/results/timing-$i.json"
  cp "$root/timing-$i/reports/chain-v4.json" "$artifacts/results/timing-$i-native.json"
  python3 - "$root/timing-$i/reports/chain-v4.json" <<'PY'
import json, sys
r=json.load(open(sys.argv[1])); hops=r['Hops'][1:]
stages={k: sum(h['BuildStagesMS'][k] for h in hops) for k in hops[0]['BuildStagesMS']}
print(json.dumps({'run':sys.argv[1], 'build_99_ms':sum(h['BuildMS'] for h in hops), 'stages_99_ms':stages, 'chain_ms':r['FastChainMS'], 'closure_error':r.get('Error')}),flush=True)
PY
done
taskset -c 0-3 env GOMAXPROCS=4 UTXO_CHAIN_BUILD_PROFILE=1 \
  UTXO_CHAIN_CPU_PROFILE="$root/client.cpu.pprof" \
  python3 "$repo/docs/experiments/baseline-design-2026-10-13/run_next.py" \
  --bin "$repo/.scratch/preparation-profile-bin" --run-dir "$root/cpu" \
  --workload chain --length 100 --count 100 --storage memory > "$artifacts/results/cpu.json"
cp "$root/cpu/reports/chain-v4.json" "$artifacts/results/cpu-native.json"
cp "$root/client.cpu.pprof" "$artifacts/results/client.cpu.pprof"
echo 'five timing chains and separate CPU profile completed'
