#!/usr/bin/env bash
set -euo pipefail
repo=/mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree
artifacts="$repo/docs/experiments/preparation-profile-2026-10-13/results"
taskset -c 0-3 env GOMAXPROCS=4 \
  UTXO_CHAIN_PROFILE_LAB=/home/qyinf/next-preparation-profile-20261013/timing-0 \
  "$repo/.scratch/preparation-profile-bin/payctl-profile.test" \
  -test.run='^$' -test.bench='^BenchmarkChainPreparation$' \
  -test.benchtime=1s -test.count=3 -test.benchmem \
  -test.cpuprofile=/home/qyinf/next-preparation-observer-ab-20261013/components.cpu.pprof \
  > "$artifacts/components.txt"
cp /home/qyinf/next-preparation-observer-ab-20261013/components.cpu.pprof "$artifacts/components.cpu.pprof"
cat "$artifacts/components.txt"
