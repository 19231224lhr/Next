#!/bin/sh
set -eu
export PATH=/usr/local/go/bin:$PATH
cd /Users/richz/lab/man/utxo-fastpay-v12/.run/opt2-verify
python3 third_party/cometbft/overlay.py
go test -tags=comet_v3 ./...
go vet -tags=comet_v3 ./...
go test -race -tags=comet_v3 ./internal/store ./internal/gateway ./cmd/gateway ./internal/transport ./cmd/member
echo VERIFICATION_COMPLETE
