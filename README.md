# UTXO FastPay

Public submissions now use `DirectSubmission` (406): transaction, existing organization spend authorization and immediate input certificates. New output TXCer remains in wallet/INSTALL delivery (405). See [the current amendment](docs/implementation-public-submission-v4.md). Use a fresh genesis for this accounting-rule revision.

Current implementation: direct-liability payments with block-driven wallet and
member updates (wire/application version 4). `main` preserves the tested
performance baseline; `re` adds the continuous-respending experiment and its results.

**Start with [current architecture, build instructions and measurements](docs/implementation-block-following-v4.md).**

## 实验索引与已验证性能

以下结果来自 Mac Studio M4 Max（16 核、64 GB）同机多进程实验，使用真实签名与四成员三票认证。性能轮采用组织/委员会应用内存状态、Comet MemDB 和钱包 bbolt NoSync，保留既有 Comet WAL/FilePV 同步；每轮重新创世，不提供崩溃恢复保证。钱包在同一压测进程内运行，不包含独立远端收款设备的网络交付时间。

| 实验 | 已验证结果 | 报告与复现材料 |
|---|---|---|
| 四委员纯共识 | 目标 3000 TPS，连续 3 分钟、54 万笔，两次全部成功；含收尾实际吞吐约 **2986 TPS**。3500 档单次通过，4000 档大样本未全部成功 | [共识容量实验](docs/experiments/consensus-capacity-2026-09-22/README.md) |
| 单担保组织完整快速支付 | 两轮各 15 万笔，目标 2200、实际发送约 2127 TPS；含公共结算和成员收尾约 **2104–2106 TPS**，快速到账 P50 **4.54–4.55 ms**、P95 **69.46–69.95 ms**；每轮约 71 秒 | [单组织整体 TPS 与单变量对照](docs/experiments/single-org-tps-2026-09-22/README.md) |
| 较低负载下的完整快速支付 | 目标 2000 TPS，两轮实际完整闭环约 **1949–1950 TPS**；快速到账 P50 **2.52–2.57 ms**、P95 **50.50–50.76 ms** | [同一报告中的 2000 档](docs/experiments/single-org-tps-2026-09-22/README.md) |
| 单笔快速付款 | 连续续花实验的 1 跳快速组三次为 **1.749 / 2.017 / 2.274 ms**，中位 **2.017 ms**；这是低负载采样，不是高负载尾延迟 | [逐轮数据与计时定义](docs/experiments/continuous-respending-2026-09-22/README.md) |
| 真实连续续花（论文实验一） | 链长 1/10/100，两种方式各三轮，**18 案例、666 笔全部通过审计**。100 跳最后钱包快速可用中位 **198.630 ms**，整链后台收尾 **785.519 ms**；逐跳等待公共确认的对照组最后钱包快速可用为 **64.384 s** | [连续续花结果、图表和原始数据](docs/experiments/continuous-respending-2026-09-22/README.md) · [实验方案](docs/research/continuous-respending-experiment-design-2026-09-22.md) |

**计时与结论范围：** 快速到账从付款钱包开始 HTTP 发送计时，到收款钱包验证 TXCer 与输出绑定、完成本地原子接收；是否耐久落盘取决于实验存储模式。完整闭环还包括公共结算观察和各成员逐事实收尾。纯共识不包含在线组织签发和钱包接收，不能用它代替整系统 TPS。上述 TPS 使用独立最终 UTXO、少量地址高复用；连续续花则使用真实前后依赖输出，不能把单链跳数/秒当系统容量。

连续续花快速组的 **324/324 次后继发送早于父交易最早应用 Commit**；96 项实际缺失输出责任均由父交易正常到达解除。固定单输入/单输出下，TXCer 为 941 字节，100 跳内未观察到单跳延迟明显增长。等待组包含钱包确认观察、成员跟块及重试成本，因此对照属于本实现的两种付款方式，不代表纯共识加速或相对其他协议的性能优势。

每份报告保留参数、独立重复、审计与复现入口。目标速率不是实际吞吐；2400 的完整系统测试触及在途上限，不列为稳定工作点。当前结果不外推至广域网、大规模地址轮换、无限链长或无限期运行。

## Current runtime

Normal settlement emits no per-payment output or member credit proofs. A shared
block follower verifies committed execution results once per height and applies
local changes atomically. Existing three-vote fast delivery, background
INSTALL, direct issuer liability and real historical input repair remain.

Member processes now open their bbolt database with **NoSync** by default for
fresh-start experiments. Transactions still apply atomically during normal
operation and votes follow the committed update, but member state is not guaranteed
durable or crash-consistent. Restart the whole experiment with fresh state after
failure. Gateway, wallet and committee application stores default to synchronous
writes unless their explicit experimental memory/NoSync modes are selected.
The startup log reports `storage_no_sync=true`. See
[the change and validation](docs/experiments/member-nosync-default-2026-09-21/README.md).

For the experimental in-memory member mode, set
`UTXO_EXPERIMENT_MEMBER_MEMORY=1`. It uses an ordered B-tree under the same
transaction boundary and signing order. A clean shutdown exports an **audit-only**
`member.db`; startup refuses audit snapshots and incomplete `.pending` files.
There is no crash recovery or resume mode. `bench-v4 -wallet-no-sync` independently
opts the benchmark wallet out of disk synchronization (the default is false).
See [full-path measurements and the all-source reproduction tool](docs/experiments/full-path-opt-2026-09-22/README.md).

The same fresh-start storage mode is available to gateways with
`UTXO_EXPERIMENT_GATEWAY_MEMORY=1`. It retains atomic updates, completed-block
checks and bounded background work, but performs no runtime gateway database
writes. Shutdown exports an audit-only `gateway.db`; failed exports are returned
as errors and are not usable for restart. The default gateway remains synchronous
bbolt. Public relay submission has 8 bounded slots (INSTALL remains 4), and
member foreground admission has 256 slots (background remains 32). These limits
absorb measured bursts; they do not remove overload or validation. See [the isolated comparisons and reproduction commands](docs/experiments/tps-opt2-2026-09-22/RESULTS.md).

Run `python3 third_party/cometbft/overlay.py` before building. Keep the existing
`-tags=comet_v3` fork build switch, but use `payctl init-lab -v4`, `bench-v4` and
`demo-v4` with a fresh genesis. Do not reuse wire-v3 databases.

For paced load, `bench-v4 -rate 200 -concurrency 256 -max-pending 2048`
releases a send permit after the receiving wallet verifies and commits
TXCer locally (durability follows the selected wallet mode), while bounded tasks continue observing public settlement and member
completion. Omit `-max-pending` to retain the original complete-lifecycle worker
pool. Reports include dispatch/permit waiting, progress-query load, per-stage
outcomes, and a 250 ms reconstruction of unfinished counts and oldest age.
See [the benchmark admission plan and experiments](docs/experiments/dispatch-lag-2026-09-20/PLAN.md).

### Single-organization whole-system experiments

`bench-v4 -same-org` sends between two wallet identities through the issuing
organization. The new bounded `POST /v4/progress` endpoint coalesces up to 128
exact fact reads in one store view. `-batch-progress` is enabled by default;
`-batch-progress=false` retains the original per-fact observer for comparisons.
Completion still requires each member's own observed/closed state, not height
or HTTP acceptance. Reports separate physical batch requests and time from
logical checks and queue waiting.

Repeated, successfully verified recipient descriptors use a bounded 1024-entry
cache keyed by their entire signed value. Network/routing rules and every
payment's owner authorization, quorum and mutable input/budget checks remain.
This particularly benefits address reuse; address-churn results are reported
separately. No new output certificate is submitted to the committee.

`UTXO_EXPERIMENT_COMMIT=250ms` optionally shortens the direct-mode height timing
for whole-system latency experiments. The default remains 500ms, overlapping
execution/Commit. This changes no quorum, proof, timeout-repair or durability rule.
The complete nine-service workload, storage modes, controls and results are in
[the single-organization TPS report](docs/experiments/single-org-tps-2026-09-22/README.md).

The prior wire-v3 baseline is commit `039374d`; its historical measurements are
in [v1.2 implementation notes](docs/implementation-v1.2.md). Current short tests
do not establish sustained high TPS or bounded long-term storage.

The sections below document the **v1.1 baseline**, not the current runtime path.

## Legacy v1.1 implementation

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

For the current wire4 path, complete payment bytes enter an optional bounded
memory inbox after the response flush. Public submission and member INSTALL may
run concurrently with gateway persistence, using the existing four slots per
action. The inbox holds at most 128 payments / 32 MiB; overflow falls back to
durable outbox scanning. Gateway retry cooldowns stay in bounded memory (8192
entries, two seconds after completion); restart may resend the same bytes early.
Outbox creation and verified completion remain durable. Member first-fallback
deadlines remain persisted and are not extended by retries or HTTP acceptance.
See [the isolated comparisons](docs/experiments/parallel-relay-2026-09-19/README.md).

After three verified votes the gateway sends the full Content-Length response
and flushes it, then hands the certificate and outbox save to a background task.
At most 128 such tasks may run; when full, the handler performs the save itself.
Concurrent saves still use the existing Group store batching. The normal handler
returns without waiting for disk, allowing the next request on its HTTP/1
connection to proceed. Shutdown closes admission and joins handlers and saves
before closing storage. Save failures retain the payment identity in error logs.
The handoff is volatile, not a durable receipt; a crash before persistence still
requires an existing full-payment holder to resubmit. This removes a connection
dependency, not disk work.

Member state commits before signing and recipient-wallet state updates before READY
remain ordered; durability follows the explicitly selected experiment mode. Demo and bench now run the existing relay over each wallet
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

The following describes the retained legacy wire3 path. Current wire4 gateway
pacing is described above and uses no retry-only database update.

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


### Backend work reduction

Member relays authenticate receipt batches once and apply all resource credits,
public custody, and outbox completion in one synchronous transaction. INSTALL
acknowledgements only suppress redundant transport; original proof and retry
requirements remain. Each relay handles at most four distinct tasks at a time.

`bench -transactions-per-lane 16 -lanes 8` generates exactly 128 payments;
zero retains the duration-based workload. Proof and credit observers have
separate bounded workers. Their queue delays are reported separately and remain
included in the observed completion latencies. `generation_elapsed_seconds`
records actual generation time, while generation-window TPS uses the configured
window; fixed-count window rates are not saturation-throughput measurements.

The [paired measurements and raw evidence](docs/performance/backend-reduction-2026-09-18/README.md)
show reduced backend drain time, with a foreground-latency tradeoff under four-way
parallelism on this single host. Continuous input still uses final anchors and
bounded queues; it does not establish a maximum sustainable TPS.
