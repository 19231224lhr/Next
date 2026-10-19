> 后续速度优化及当前结论见 [固定入口](../../design/same-org-optimization-status.md)。以下保留原轮次数据。

# Certificate delivery and internal funds

This directory records two independently enabled changes on top of `7a2ab17`.
They have different effects: direct delivery removes the sender-forwarding
dependency, while internal funds reuse a publicly locked balance across transfers.

## 本轮结果

- **证书直达已实现。** 在证书已经生成后的交付测试中，人为给付款人返回路径加入
  20 ms 等待时，收款钱包完成核验和保存的中位时间从 25.429 ms 降到 7.064 ms。
  不加这段等待时，旧路径为 2.757 ms，直达为 5.044 ms；直达队列的持久保存有成本。
  这是本地 HTTP 交付测试，不能当作整笔付款或真实广域网的提速比例。
- **组内资金复用已实现为独立实验模式。** 锁入 100 CAL 后，三名用户连续转手
  100 次，托管金额始终为 100，未新增普通付款的 CAL 担保预留。退出后生成
  100 CAL 普通输出，并成功申请下一笔普通付款。
- 对照的普通证书链在这 100 笔期间不进行公开结算，每个成员的本地预留计数
  都达到 10,000 CAL。这是各成员的账本记录，不能把四个计数相加当作全局资金。
  新机制用提前锁款和受限的组内流转换取这一节省。
- 同步持久存储的本机测试中，普通路径单笔中位时间为 9.0345 ms，组内池为
  12.8788 ms。组内池还包括完整证书的两份保存确认。本轮没有测出单笔速度提升。

本轮通过 `go test ./...`、`go build ./cmd/...`、相关包 race 检查和独立代码复核。
组内集成测试共 14 项，覆盖普通付款冲突、旧持有人退出、转账/退出并发、重启和
证书恢复。代码位于现有 `fix/partial-approval-reclamation` 工作分支，与后续速度优化一起提交。
原始数据及不同计时边界见下方两份报告；论文未改动。

## 1. Deliver certificates to receiving wallets

The gateway stores the complete ordinary payment certificate in a durable
delivery queue. A worker sends it directly to the receiving wallet while the
gateway returns the sender's response. The wallet still verifies the complete
certificate and saves the output before acknowledging it. Offline delivery is
retried; repeated delivery does not create another output.

Enable `-direct-recipient-delivery` on `cmd/gateway`, configure `RecipientRoutes`,
and enable `DirectDelivery` on participating E7 wallets. Routes are set by the
operator, including routes for change outputs. They cannot be supplied as an
arbitrary callback URL by the sender. See [DELIVERY.md](DELIVERY.md) for exact
configuration, TLS support, tests and per-sample measurements.

This feature also works across organizations. It does not change quorum size,
reserve requirements, public execution, or the validity of the certificate.

## 2. Reuse funds within one organization

The new pool protocol follows this sequence:

1. **Enter:** consume an existing final CAL output and lock its full value in
   public escrow. Members must observe successful public execution before they
   approve an internal transfer.
2. **Transfer:** the current owner signs a transfer to another wallet in the same
   organization. Three members certify the next owner. The complete certificate
   is stored at two members before the collector reports success. The recipient
   verifies and stores the internal receipt. The escrow value stays unchanged.
3. **Exit:** the owner obtains a separate exit certificate. Transfer and exit
   compete for the same predecessor, so an old owner cannot independently cash
   out an earlier receipt. Public execution consumes the escrow and creates one
   ordinary final CAL output. Replaying the exit cannot create another output.

This is a separate receipt type. It is not an ordinary `DirectPayment`, and an
internal transfer receipt cannot itself be redeemed publicly. The first version
supports one indivisible unit with a fixed organization configuration. It does
not split or merge units, rotate pool membership, or charge FUEL. Its capital
saving comes from an upfront public lock and restricted internal ownership
changes; it must not be presented as a free reduction of ordinary certificate
backing.

### Run the laboratory

From the repository root, run:

```powershell
go test ./internal/poollab -count=1 -v
$poolDirectory = Join-Path $env:TEMP ('next-pool-' + [guid]::NewGuid().ToString())
go run ./cmd/pool-lab -transfers 100 -store-dir $poolDirectory
```

The directory must be fresh. The command uses four real HTTP member handlers,
three wallets and synchronous stores. It executes real deposit/exit application
transitions and verifies signed laboratory blocks. It does not run a validator
consensus network or measure public confirmation latency. See
[POOL-TESTS.md](POOL-TESTS.md) for the exact evidence and timing boundary.

### Enable on a separate development network

Set `EnableInternalPools: true` in a **new** network configuration. This setting
is included in the public engine's configuration identity and the member store
identity. Existing stores must not be relabeled or reused to activate it.
`cmd/member` forces synchronous storage for pools and rejects its memory-mode
environment override. Ordinary behavior remains the default when the flag is
absent.

`cmd/poolctl` provides a durable, file-based client for a local development
network. `-network`, `-key`, and `-wallet-dir` are required. Supply a `PoolAction`
JSON with `-action`, a previous certificate with `-parent` for transfer/exit,
and an output certificate path with `-out`. The command fills the sender and
owner signature locally. A deposit identifies a known final input and its
creation evidence; a transfer/exit identifies the pool, next sequence and exact
previous action. The amount is unchanged. Recipient descriptors use the
existing signed wallet descriptor format.

Use `-submit` only for deposit/exit. Submission acceptance is not finality:
members still need to follow the resulting public block. Use `-receive` to
verify and save a transfer receipt at the receiving wallet. Keep the original
signed request and wallet directory for retries. These clients use the existing
loopback HTTP transport; they do not provision a remote TLS deployment.

### Recovery rules

A retry uses the same authorized request. Members retain their vote history,
and a valid newer quorum certificate can advance a lagging member past a losing
minority vote. A finalized public winner resolves conflicts between ordinary
payment and pool entry on the shared input lock, with any losing ordinary
reservation released atomically.

Successful certificate storage at two distinct members leaves at least one
honest copy under the one-Byzantine assumption. Recovery can retrieve the full
certificate by its action identity instead of relying on every original signer
to resend a vote. A crash before this storage step finishes is not reported as
success. Conflicting requests can still split votes and stall; there is no
timeout unlock or automatic withdrawal from stale state.

No live funds or Mac deployment are part of these local measurements. The
implementation is committed together with the subsequent speed work; see
[the current implementation summary](../../design/same-org-optimization-status.md).
