#!/usr/bin/env bash
set -euo pipefail
repo=/mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree
artifacts="$repo/docs/experiments/preparation-profile-2026-10-13"
root=/home/qyinf/next-preparation-observer-ab-20261013
mkdir -p "$artifacts/results" "$root"
run_one() {
  name="$1"; events="$2"; cpu="$3"
  taskset -c 0-3 env GOMAXPROCS=4 UTXO_CHAIN_BUILD_PROFILE=1 \
    UTXO_CHAIN_COMMIT_NOTIFICATIONS="$events" UTXO_CHAIN_CPU_PROFILE="$cpu" \
    python3 "$repo/docs/experiments/baseline-design-2026-10-13/run_next.py" \
    --bin "$repo/.scratch/preparation-profile-bin" --run-dir "$root/$name" \
    --workload chain --length 100 --count 100 --storage memory \
    > "$artifacts/results/$name.json"
  cp "$root/$name/reports/chain-v4.json" "$artifacts/results/$name-native.json"
  python3 - "$root/$name/reports/chain-v4.json" <<'PY'
import json,sys
r=json.load(open(sys.argv[1])); hs=r['Hops'][1:]
stages={k:sum(h['BuildStagesMS'][k] for h in hs) for k in hs[0]['BuildStagesMS']}
save={k:sum(h['BuildSaveStagesMS'][k] for h in hs) for k in hs[0]['BuildSaveStagesMS']}
print(json.dumps({'run':sys.argv[1],'build_99_ms':sum(h['BuildMS'] for h in hs),'stages':stages,'save':save,'checks':sum(h['FinalChecks'] for h in r['Hops']),'chain_ms':r['FastChainMS'],'error':r.get('Error')}),flush=True)
PY
}
for i in 0 1 2; do
  if (( i % 2 == 0 )); then
    run_one "ab-$i-polling" 0 ""
    run_one "ab-$i-events" 1 ""
  else
    run_one "ab-$i-events" 1 ""
    run_one "ab-$i-polling" 0 ""
  fi
done
run_one "cpu-polling" 0 "$root/polling.cpu.pprof"
run_one "cpu-events" 1 "$root/events.cpu.pprof"
cp "$root/polling.cpu.pprof" "$artifacts/results/polling.cpu.pprof"
cp "$root/events.cpu.pprof" "$artifacts/results/events.cpu.pprof"
echo 'observer A/B complete'
