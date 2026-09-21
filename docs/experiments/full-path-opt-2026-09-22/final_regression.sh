#!/bin/sh
set -eu
export PATH=/usr/local/go/bin:$PATH
cd /Users/richz/lab/man/utxo-fastpay-v12
python3 third_party/cometbft/overlay.py
go test -tags=comet_v3 ./cmd/... ./internal/... ./protocol/... ./crypto/... ./finality/...
go vet -tags=comet_v3 ./cmd/... ./internal/... ./protocol/... ./crypto/... ./finality/...
go test -race -tags=comet_v3 ./internal/store ./internal/member ./internal/gateway ./cmd/member ./internal/transport
go test github.com/cometbft/cometbft/consensus -run '^TestProposalBatch' -count=1
go test github.com/cometbft/cometbft/libs/autofile -run 'TestSyncTracking|TestGroupSyncAfterBufferedFlushAndRotation|TestConcurrentGroupWritesAndSync' -count=1
go test github.com/cometbft/cometbft/state -run '^TestFinalizeResults' -count=1
go test github.com/cometbft/cometbft/store -run '^TestCatchupCandidate' -count=1
printf 'FINAL_REGRESSION_PASS\n'
