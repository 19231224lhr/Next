#!/usr/bin/env bash
set -euo pipefail
repo=/mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree
artifacts="$repo/docs/experiments/preparation-optimization-2026-10-13"
root=/home/qyinf/next-preparation-optimization-20261013
mkdir -p "$artifacts/results" "$root"
run_next() {
  i="$1"; arm="$2"
  taskset -c 0-3 env -u UTXO_CHAIN_COMMIT_NOTIFICATIONS -u UTXO_CHAIN_CPU_PROFILE \
    -u UTXO_CHAIN_CPU_FOREGROUND_ONLY GOMAXPROCS=4 UTXO_CHAIN_BUILD_PROFILE=1 \
    python3 "$repo/docs/experiments/baseline-design-2026-10-13/run_next.py" \
    --bin "$repo/.scratch/preparation-$arm-bin" --run-dir "$root/r$i-$arm" \
    --workload chain --length 100 --count 100 --storage memory \
    > "$artifacts/results/r$i-$arm.json"
  cp "$root/r$i-$arm/reports/chain-v4.json" "$artifacts/results/r$i-$arm-native.json"
  python3 - "$root/r$i-$arm/reports/chain-v4.json" <<'PY'
import json,sys
r=json.load(open(sys.argv[1])); hs=r['Hops'][1:]
print(json.dumps({'run':sys.argv[1], 'build99_ms':sum(h['BuildMS'] for h in hs), 'encode99_ms':sum(h['BuildStagesMS']['encode_request'] for h in hs), 'checks':sum(h['FinalChecks'] for h in r['Hops']), 'chain_ms':r['FastChainMS'], 'reuse':r.get('SavedRequestBytesReused',False), 'notifications':r.get('CommitNotifications',False), 'error':r.get('Error')}),flush=True)
PY
}
run_fastpay() {
  i="$1"
  taskset -c 0-3 python3 "$repo/docs/experiments/baseline-design-2026-10-13/run_fastpay.py" \
    --bin /home/qyinf/.local/share/next-baseline-tools/fastpay-target/release \
    --run-dir "$root/r$i-fastpay" --workload chain --length 100 --count 100 \
    --protocol Tcp --client-mode receiver-confirmed > "$artifacts/results/r$i-fastpay.json"
  cp "$root/r$i-fastpay/native-client.json" "$artifacts/results/r$i-fastpay-native.json"
  python3 - "$artifacts/results/r$i-fastpay.json" <<'PY'
import json,sys
r=json.load(open(sys.argv[1])); print(json.dumps({'run':sys.argv[1],'chain_ms':r['chain_total_ms'],'completed':r['completed'],'principal_conserved':r['principal_conserved'],'probe_completed':r['probe_completed']}),flush=True)
PY
}
for i in 0 1 2 3 4; do
  case "$i" in
    0) order='before after fastpay';;
    1) order='fastpay after before';;
    2) order='before fastpay after';;
    3) order='after before fastpay';;
    4) order='fastpay before after';;
  esac
  for arm in $order; do
    if [[ "$arm" == fastpay ]]; then run_fastpay "$i"; else run_next "$i" "$arm"; fi
  done
done
taskset -c 0-3 env -u UTXO_CHAIN_COMMIT_NOTIFICATIONS -u UTXO_CHAIN_CPU_PROFILE \
  GOMAXPROCS=4 python3 "$repo/docs/experiments/baseline-design-2026-10-13/run_next.py" \
  --bin "$repo/.scratch/preparation-after-bin" --run-dir "$root/independent-smoke" \
  --workload independent --length 100 --count 30 --storage memory \
  > "$artifacts/results/independent-smoke.json"
cp "$root/independent-smoke/reports/bench-v4-0.json" "$artifacts/results/independent-smoke-native.json"
echo 'five matched rounds and independent sender smoke complete'
