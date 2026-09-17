# UTXO FastPay

Research implementation of protocol v1.1 (wire/protocol version 2).
Primary development checkout: Mac Studio, `/Users/richz/lab/man/utxo-fastpay`.

## Current implementation

- Canonical binary transaction/certificate envelopes, Ed25519 owner/recipient/quorum signatures.
- Checked amounts, per-grant rounding, cumulative accounting and bounded fee lifecycle rules.
- Atomic memory/bbolt stores with independent identities and normal same-database restart.
- Durable member approval, three-vote certificates, background INSTALL, direct-parent import.
- Same/cross-organization pre-settlement spending, persistent conflict locks and local budgets.
- CometBFT 0.38 with typed Merkle facts, next-height authenticated proofs and real four-node tests.
- Ordinary CAL payment settlement, deferred child registration, finite FUEL reserves and idempotent fee stages.
- Proof-driven FUEL/policy/E member credits and finalized-output import.
- Retail direct transfers with atomic CAL/FUEL consumption and deterministic fee change.

This is an implementation in progress, **not a completed payment system**.
The core ordinary-payment path is tested through real consensus and proof-driven FUEL
credit. Independent member/committee/gateway processes, durable wallets, background relays and
public-custody replacement are available for the ordinary-payment laboratory.
Root fulfilment, real refill/reward claims, physical archive, full benchmark telemetry
and sustained experiments remain in progress.
No public service or benchmark TPS is claimed.

## Development

Use Go 1.27.1. On this Mac the binary is `/usr/local/go/bin/go`.
If the default module proxy is unreachable, use a per-command
`GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct`; checksum verification stays enabled.

```sh
go test ./...
go test -race ./...
go test -race -tags=integration ./internal/testkit -count=1
go vet ./...
go build ./...
go test ./protocol -run='^$' -fuzz=FuzzTransaction -fuzztime=10s -parallel=2
```

Tests use temporary independent node databases with synchronous persistence and real signatures.
Genesis fixtures are finite trusted laboratory allocations; they are not incoming-deposit credit.
Current executable paths reject unsupported transaction features rather than simulating success.

The governing documents are in [docs/design](docs/design).
Actual module status and limitations are maintained in [docs/progress.md](docs/progress.md).

## Local multiprocess laboratory

Build and initialize once; initialization refuses to overwrite an existing lab:

```sh
go build -o bin/ ./cmd/member ./cmd/committee ./cmd/gateway ./cmd/payctl
mkdir -p experiments
bin/payctl init-lab -dir experiments/lab -outputs 1024 -port 18000
bin/payctl lab-run -dir "$PWD/experiments/lab" -bin "$PWD/bin"
```

Run `bin/payctl demo -dir experiments/lab -hops 8 -input 0` from another terminal.
Use a fresh unused input index for each independent demo. The laboratory starts two
four-member organizations, four committee members and two gateways (14 independent
processes). Loopback is the default; non-loopback HTTP listeners require mutual TLS.
The demo persists wallet requests before submission and received certificates before
the next spend. Final proof sampling currently happens after building the fast chain,
so those timestamps include observation delay and must not be called settlement latency.

Stop the lab-run supervisor with Ctrl-C to gracefully stop its children, then run
`bin/payctl audit -dir experiments/lab`. Audit opens stopped databases read-only and
checks CAL/FUEL supply, fee escrow conservation, and member slices against original
debits. It reports remaining outboxes instead of silently treating them as completed.

On the installed macOS screen 4.00 use `screen -L -dmS utxo-lab /absolute/bin/payctl lab-run ...`;
the newer `-Logfile` option is unavailable. Reports and all laboratory keys/databases
stay under gitignored `experiments/`. Keys are randomly generated, not the deterministic
identities used by unit test fixtures.
