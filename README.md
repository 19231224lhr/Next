# UTXO FastPay

Research implementation of protocol v1.1 (wire/protocol version 2).
Primary development checkout: Mac Studio, `/Users/richz/lab/man/utxo-fastpay`.

## Current implementation

- Canonical binary transaction/certificate envelopes, Ed25519 owner/recipient/quorum signatures.
- Checked amounts, per-grant rounding, cumulative accounting and bounded fee lifecycle rules.
- Atomic memory/bbolt stores with independent identities and normal same-database restart.
- Durable member approval, three-vote certificates, background INSTALL, direct-parent import.
- Same/cross-organization pre-settlement spending, persistent conflict locks and local budgets.
- CometBFT 0.38 ABCI commit-boundary adapter and replay tests.

This is an implementation in progress, **not a completed payment system**.
The ABCI adapter currently tests execution/commit isolation; payment commands, typed fact
roots/proofs, real four-validator integration and the complete settlement/credit feedback
loop remain to be connected. No public service or benchmark TPS is claimed.

## Development

Use Go 1.27.1. On this Mac the binary is `/usr/local/go/bin/go`.
If the default module proxy is unreachable, use a per-command
`GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct`; checksum verification stays enabled.

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./...
go test ./protocol -run='^$' -fuzz=FuzzTransaction -fuzztime=10s -parallel=2
```

Tests use temporary independent node databases with synchronous persistence and real signatures.
Genesis fixtures are finite trusted laboratory allocations; they are not incoming-deposit credit.
Current executable paths reject unsupported transaction features rather than simulating success.

The governing documents are in [docs/design](docs/design).
Actual module status and limitations are maintained in [docs/progress.md](docs/progress.md).
