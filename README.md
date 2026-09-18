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
- On-demand blocks: idle consensus waits for transactions; necessary proof/maintenance blocks finish before it becomes idle again.
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

## Measurement timing

New demo and bench reports use timing origin `wallet_http_submit_v2`: the instant
before the paying wallet calls HTTP Client.Do, after saving the signed request
and encoding it. Wallet-ready latency ends after recipient verification and
synchronous certificate persistence. HTTP connection setup and transport waiting
are included; sender preparation, signing and request persistence are excluded.
Final-proof and credit observations use the same start. The generation window and
whole-run wall time still include workload preparation and waiting. Both wallets
are simulated in one process; there is no separate device-to-device delivery hop.
Older reports without this marker start before sender request persistence and
retain their original meaning; do not mix their latency samples with v2 reports.

### Single-payment stage trace

Run `bin/payctl demo -dir experiments/lab -hops 1 -input UNUSED -trace`.
Tracing is off by default. With tracing enabled, request-local timestamps are
returned in bounded diagnostic HTTP headers and stored in the demo JSON report.
Stages include gateway/member handler entry, decoding, validation, entry into
storage, atomic state checks, successful commit return, vote signing, quorum
collection, certificate persistence and recipient-wallet persistence.
The collector still returns at three valid votes; the fourth member may be absent
from the returned snapshot. These are observations, not signed protocol evidence.

`unix_ns` permits a joint timeline only on the same host (without a clock step).
`local_ns` uses a monotonic clock relative to that process's request recorder;
it must not be subtracted across different nodes. Handler entry follows HTTP
parsing, and response-ready precedes the actual socket write. Storage timing
includes scheduling, group-commit waiting and database work; it is not pure
fsync duration. Request bodies, signatures and keys are not included in the trace.
Enabling tracing adds timestamp/serialization and response-byte overhead, so
use it to locate costs and repeat final latency comparisons with tracing disabled.

### Response before gateway persistence

After three verified votes the gateway sends the full Content-Length response
and flushes it, then persists the certificate and outbox in the same bounded
handler. The existing 128 handler slots also bound pending persistence; no new
worker queue or unbounded goroutines are introduced. Slow background writes can
still occupy slots or HTTP/1 connections and limit sustained throughput; this
change removes the single-payment foreground dependency, not disk work.

Member persistence before signing and recipient-wallet persistence before READY
remain unchanged. Demo and bench now run the existing relay over each wallet
outbox. To resume an offline wallet's pending certificates without making a new
payment, run `bin/payctl wallet-relay -dir experiments/lab -owner 0` (or owner 1).
Do not open the same wallet database in another process simultaneously.
The wallet relay contacts issuer members and the committee directly. If all full
certificate holders are offline, progress may pause; unresolved locks are retained.

### Single-payment block observation

Add `-observe-block` to a one-hop demo to poll committee 0's committed SETTLED
state every 10 ms before obtaining the final output proof. CommitObservedMicros
measures the first successful committed-state observation from wallet HTTP
submission, including query/polling delay; it is not the exact consensus instant.
SettlementHeight and ProofHeaderHeight come from the subsequently verified proof.
With the present proof scheme the latter equals the former plus one.
This separates observed transaction-block commitment from next-height proof
availability without changing consensus or settlement behavior.

### Optional backend settlement timing

Start the lab supervisor and demo with `UTXO_SETTLEMENT_TRACE=1`, then run a
one-hop `demo -trace -observe-block`. The in-memory recorder retains at most
2048 delivery attempts per process, keyed by the SHA-256 of the actual submitted
command (including its delivery nonce). It changes no signed bytes or ledger
state and is disabled by default. Delivery HTTP headers carry client submission
time; committee HTTP entry/acceptance, rule execution and application Commit
are recorded separately. Committee `/debug/settlement/{spend}` exists only while
enabled. Demo embeds committee 0's snapshot after verifying the final proof.

Correlate execution with the authenticated SettlementHeight, not merely the
earliest retry. These are unauthenticated same-host observations; use calibrated
clocks before comparing machines. Commit marks successful application database
commit, not completion by all committee members. The acceptance-to-execution
interval includes consensus scheduling; it does not identify individual Comet
round stages. Duplicate delivery attempts do not represent separate payments.

### Optional consensus phase diagnostics

With `UTXO_SETTLEMENT_TRACE=1`, `/debug/consensus` exposes a bounded 4096-event
in-memory history of selected Comet stages and ABCI boundaries. Per-attempt
settlement records also include proposal selection and receipt times. These are
local observations, not finality proofs.

`UTXO_EXPERIMENT_FLUSH=10ms` and `UTXO_EXPERIMENT_GOSSIP=10ms` override the
corresponding Comet propagation intervals for controlled experiments. Without
these variables the defaults remain 100 ms; no consensus timeout or durability
setting is changed. See [the phase diagnosis](docs/experiments/latency-phase-2026-09-18/README.md)
for measured results, limitations and the separate delivery-retry repair plan.

### Durable relay retry pacing

Relays keep the delivery envelope and retry timing in the existing outbox. Proof
polling does not itself resubmit a payment. Retries are spaced by one second;
only five seconds without verified completion permit a new delivery nonce. An
identical certificate uses the same initial envelope across its holders. The
immutable payment identity and accounting remain unchanged. A verified terminal
work receipt stops resubmission while remaining proofs continue to be fetched;
the original custody/credit requirements still govern queue retirement.

Proof requests have their own timeout, so an unavailable proof endpoint does not
consume the entire delivery deadline. HTTP acceptance and cache hits never release
budget or permanently retire a pending payment. See the [fresh-genesis comparison](docs/experiments/relay-retry-2026-09-18/README.md)
for measured latency, command amplification, and restart verification.
