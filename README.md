# UTXO FastPay

### 基于担保组织的 UTXO 快速支付实验系统

收款钱包验证 **TXCer** 后即可继续付款，无需等待前置交易完成公共结算。担保组织负责快速认证与直接担保，委员会负责公共账本、资金结算和异常赔付，钱包与组织成员自行跟块更新状态。

[性能结果](#性能结果) · [快速开始](#快速开始) · [文档导航](#文档导航) · [实验配置](#实验配置) · [开发与验证](#开发与验证)

> 当前运行协议为 **wire 4**，项目定位为可复现的研究原型。
> `main` 保留性能基线，`re` 汇总已完成的 E1 连续续花与 E2 资金周转实验，包含用户自付 FUEL、CAL 动态补资、报告及原始数据。E3 目前仅有实验方案，尚未实施。
> `e2-budget`、`e2-owner-fuel`、`e2-adaptive-reserve` 保留各阶段记录，其成果已合并到 `re`。

## 系统概览

| 组件 | 职责 |
| :--- | :--- |
| **钱包** | 签署付款，验证 TXCer 与输出绑定，接收后续花；跟块确认输出并更新本地状态 |
| **担保组织** | 网关分发请求；四个成员独立验证，取得三份有效签名后交付 TXCer |
| **后台处理** | INSTALL 保存、传播完整材料；中继提交公共交易，成员在有限延迟后备用补投 |
| **担保委员会** | 四委员运行 CometBFT，裁决输入消费、执行资金与费用规则、处理直接责任及必要赔付 |

**快速到账与公共结算分开推进。** 钱包不等待后台 INSTALL 回执即可续花。委员会不接收本笔新输出的 TXCer；公共提交保留交易、组织消费授权，以及实际使用的未确认输入对应的 TXCer，不携带完整祖先历史。

**状态由各参与方自行跟块更新。** 委员会正常出块，不生成逐笔收款通知或成员核销回执。成员只恢复自己确实占用、且已满足解除条件的额度；实际赔付本金与实际费用仍是支出。

**本金与费用分工。** CAL 用于支付本金、备付及赔付；FUEL 用于 Gas、节点报酬和担保服务费。用户可以使用自己的最终 FUEL UTXO 支付，未用预留退回用户；组织代付是独立的可选分支。详见[用户自付实现](docs/implementation-owner-fuel-v4.md)。

实现细节见[按块处理规范](docs/implementation-block-following-v4.md)与[公共提交修订](docs/implementation-public-submission-v4.md)。

## 性能结果

下表是**已完成实验的工作点**，不是理论上限。原始数据、对照条件与复现脚本均在对应报告中。

### 快速到账与吞吐

| 场景 | 测量规模 | 实际结果 | 证据 |
| :--- | :--- | :--- | :--- |
| 单笔快速付款 | 三次独立单笔 | 中位 **2.017 ms**；三次为 1.749 / 2.017 / 2.274 ms | [单笔与续花数据](docs/experiments/continuous-respending-2026-09-22/README.md) |
| 单组织完整系统，目标 2000 TPS | 每轮 15 万笔，重复两轮 | 完整闭环 **1949–1950 TPS**；到账 P50 **2.52–2.57 ms**，P95 **50.50–50.76 ms** | [整体 TPS 报告](docs/experiments/single-org-tps-2026-09-22/README.md) |
| 单组织完整系统，目标 2200 TPS | 每轮 15 万笔，重复两轮；每轮约 71 秒 | 实际发送约 2127 TPS；完整闭环 **2104–2106 TPS**；到账 P50 **4.54–4.55 ms**，P95 **69.46–69.95 ms** | [整体 TPS 报告](docs/experiments/single-org-tps-2026-09-22/README.md) |
| 四委员纯共识，目标 3000 TPS | 每轮 3 分钟、54 万笔，重复两轮 | 全部成功；含收尾实际吞吐约 **2986 TPS** | [共识容量报告](docs/experiments/consensus-capacity-2026-09-22/README.md) |

上述 TPS 与 E1 数据采用其原有组织代付配置。新增用户自付路径已做 E2 功能及周转验证，尚未重新测量相同的最高 TPS。

完整系统的 2400 档触及在途任务上限，不作为稳定承接 2400 TPS 的证据。纯共识 3500 档单次通过，4000 档大样本未全部成功。

### 真实连续续花 · 论文实验一

三个独立钱包按 A → B → C → A 转手，每次收到并验证上一笔 TXCer 后，才构造下一笔交易。链长 1、10、100，各与“等待公共确认后再付款”对照，每种设置独立重复三次。

**18 个案例、666 笔付款全部完成并通过审计。** 下表为三轮中位数，终点均为最后一跳钱包快速可用。

| 交易链长度 | 收到即可续花 | 每跳等待公共确认 |
| :--- | ---: | ---: |
| 1 笔 | 2.017 ms | 2.004 ms |
| 10 笔 | 19.878 ms | 5.655 s |
| 100 笔 | **198.630 ms** | **64.384 s** |

- **真实未确认消费：** 快速组 324/324 次后继发送早于父交易最早应用 Commit；96 项实际缺失输出责任均随父交易正常到达而解除。
- **后台正常收尾：** 100 跳整链收尾中位时间为 785.519 ms；委员会状态一致，资金、费用与成员额度审计通过。
- **单笔材料不携带祖先链：** 固定单输入、单输出结构下，TXCer 始终为 941 字节；100 跳内未观察到单跳延迟明显增长。

[查看实验报告与图表](docs/experiments/continuous-respending-2026-09-22/README.md) · [查看实验方案](docs/research/continuous-respending-experiment-design-2026-09-22.md)

### 有限资金与权限周转 · 论文实验二

原 E2 报告采用组织代付配置，固定 20 个独立两跳单位/秒，每笔 100 CAL，分别测试 CAL、组织代付 FUEL 和工作权限的低、中、高三档，每组输入五分钟；通过父延迟公共提交建立真实的未确认输入责任场景，并记录实际触发比例。每成员使用一个 Worker，隔离额度周转与 Worker 分配。

- **中高预算能够周转：** 原矩阵六个中高档各完成 6,000 个两跳单位，临时占用正常释放。
- **低预算暴露活性边界：** 不同交易分别得到少量成员批准，却没有完整凭证，可能占满签署权限；公共结算完成不等于这些局部批准已解除。
- **单入口实验性改善：** 同入口对照中，顺序取证将低 CAL 的闭环单位从 86 增至 3,203、低工作权限从 265 增至 3,903，两组部分批准残留归零；该模式默认关闭，不能替代多网关活性设计或直接套用上面的最大 TPS。
- **代付费用不会循环恢复：** 原组织代付 FUEL 低档最终受累计实际支出和下一笔最大预留约束；正常、赔付及重复投递的费用审计分别记录。

这项实验用于解释资金、权限和责任的周转边界，不测最高吞吐，也不证明市场盈利。完成量、未启动数量、等待时间与实际责任比例均同时报告。

[查看 E2 完整报告、图表与数据](docs/experiments/finite-budget-2026-09-23/README.md) · [查看 E2 方案](docs/research/finite-budget-experiment-design-2026-09-23.md)

### E2 复测 · 用户自付 FUEL

按实际业务补齐用户最终 FUEL UTXO 支付，组织不持有代付资金或 FUEL/Policy 授权，用户费用输入、找零和退款均做真实账本核对。

- **五分钟周转：** CAL 中/高档与 Execution 高档各完成 6,000 个两跳单位、12,000 笔付款；组织代付 FUEL 均为零。
- **边界仍存在：** Execution 中档完成已启动的 2,325 个单位，CAL 中档父延迟 3 秒完成 1,792 个；未承接全部 6,000 个计划单位。低档仍有部分批准占用，不能归因于组织手续费耗尽。
- **费用清楚区分：** 普通每笔用户实际付 94 FUEL，预留 1,000 中退回 906；未成证费用输入锁定单列，不算实际支出。正常、赔付、重投及最终退款再使用均有验证。

[用户自付 E2 完整报告、图表与数据](docs/experiments/owner-fuel-2026-09-23/README.md) · [实现说明](docs/implementation-owner-fuel-v4.md)

### E2 扩展 · 按签署水位动态补资

根据成员实际可用权限、声明的输入需求和未成证请求量计算预警水位；低于水位时，从有限外部账户真实转入 CAL，再通过公共区块增加原授权。保留正常并行取证、原批准与输入锁，不开启串行取证。

三个动态场景均从 **3,600 CAL** 启动，各输入五分钟并完成 **6,000 个两跳单位、12,000 笔付款**：

| 动态场景 | 最终组织 CAL | 完成单位 | 最终部分批准残余 |
| :--- | ---: | ---: | ---: |
| 恒定 20 两跳/秒 | 14,400 | 6,000 / 6,000 | 0 |
| 10 → 30 两跳/秒 | 21,600 | 6,000 / 6,000 | 0 |
| 父公共提交延迟 3 秒 | 23,300 | 6,000 / 6,000 | 0 |

固定低额对照仅完成 46 个单位；三个固定足额对照均完成全部单位。全部完整组通过四委员一致性、原始扣账和资金审计，费用由用户支付。阶跃动态组发生 12 次成员额度不足调用，原请求重试后全部完成，因此不宣称零瞬时拒绝。

三个动态组快速到账 P95 为 **1.609～1.714 ms**，含300秒输入期及收尾的完整用时为 **301.741～303.674秒**。每组用户实际支付1,128,000 FUEL，未用预留10,872,000 FUEL全部退回；组织代付为零。详细数值及计时口径见报告中的[补资与收尾耗时](docs/experiments/adaptive-reserve-2026-09-23/README.md#补资与收尾耗时)和[费用审计](docs/experiments/adaptive-reserve-2026-09-23/README.md#费用审计汇总)。

**验证通过的是所测条件下的 CAL 动态调节。** 补资源预先备有真实资金，组织余额降低不等于总资本节省；需求采用已知实验输入轨迹，每配置运行一次。这不改变低工作权限的原有边界，也不是最高 TPS 测试。

[动态补资完整报告、图表与原始数据](docs/experiments/adaptive-reserve-2026-09-23/README.md) · [实现规范](docs/implementation-adaptive-reserve-v4.md)

### 如何理解这些数字

| 指标 | 起点与终点 |
| :--- | :--- |
| **快速到账** | 付款钱包开始 HTTP 发送 → 收款钱包验证 TXCer 与输出绑定、完成本地原子接收 |
| **完整闭环** | 包含快速到账、公共结算观察，以及各成员按确切交易事实完成本地收尾 |
| **纯共识 TPS** | 仅测委员会接纳和成功上链，不包含在线组织签发及钱包接收 |
| **整链耗时** | 第一笔发送 → 最后一跳对应终点；包含中间交易的构造、签名与等待 |

所有结果来自 **Mac Studio M4 Max（16 核、64 GB）同机多进程**环境。上述性能轮采用组织及委员会应用内存状态、Comet MemDB 和钱包 bbolt NoSync，保留既有 Comet WAL/FilePV 同步。每轮重新创世，不提供崩溃恢复保证；钱包在同一压测进程内运行，不包含独立远端收款设备的网络交付时间。

TPS 实验使用独立最终 UTXO、少量地址高复用；续花实验使用真实依赖输出。等待确认组还包含钱包确认观察、成员跟块及重试成本，因此表中差距属于本实现的两种付款方式对比。单笔延迟、单链速度和系统吞吐不能相互替代，也不外推为广域网、大规模地址轮换或无限期运行表现。

## 快速开始

需要 **Go 1.27.1** 与 **Python 3**。以下命令适用于 macOS / Linux shell，在仓库根目录执行。

### 1. 构建

```sh
python3 third_party/cometbft/overlay.py
mkdir -p bin experiments
go build -tags=comet_v3 -o bin/ ./cmd/committee ./cmd/member ./cmd/gateway ./cmd/payctl
```

`comet_v3` 是沿用的编译开关名称，用于启用受限 CometBFT 补丁；当前运行协议仍为 **wire 4**。

### 2. 初始化并启动实验网

```sh
bin/payctl init-lab -v4 -dir experiments/local-v4 -port 25000 -outputs 1024

UTXO_EXPERIMENT_FLUSH=10ms \
UTXO_EXPERIMENT_GOSSIP=10ms \
bin/payctl lab-run -dir "$PWD/experiments/local-v4" -bin "$PWD/bin"
```

初始化拒绝覆盖已有实验目录。默认实验网包含两个组织、八个成员、两个网关和四个委员，共 14 个服务进程；上面的单组织性能实验使用九个服务，部署条件不同。此快速开始沿用默认存储配置，不等同于性能报告中的内存模式。

### 3. 在另一终端发送一笔付款

```sh
bin/payctl bench-v4 -dir experiments/local-v4 -start 1 -count 1 -concurrency 1
```

每次独立测试应选用未消费的初始输入。需要阶段诊断、连续续花或 TPS 复现时，使用对应[实验报告](#文档导航)中的参数和脚本。

### 4. 停止并审计

在启动实验网的终端按 `Ctrl+C`，等待所有子进程完成停机和审计快照导出，再执行：

```sh
bin/payctl audit -dir experiments/local-v4
```

审计核对 CAL/FUEL、费用、成员原始占用及待办状态。内存模式导出的数据库仅供审计，不用于恢复运行；下一轮使用新的实验目录。

## 文档导航

### 设计与实现

| 文档 | 内容 |
| :--- | :--- |
| [系统设计](docs/design/utxo-fast-payment-system-design-final.md) | 整体协议与设计基础 |
| [Go 工程架构](docs/design/utxo-go-engineering-architecture-v1.0.md) | 模块、数据结构与工程组织 |
| [开发执行计划](docs/design/utxo-development-execution-plan-v1.0.md) | 功能模块及开发验证流程 |
| [直接责任修订](docs/design/utxo-direct-liability-amendment-v1.2.md) | 当前输入责任、赔付与受限历史输入修订 |
| [按块处理实现](docs/implementation-block-following-v4.md) | 钱包与成员跟块、执行结果验证及本地状态应用 |
| [公共提交修订](docs/implementation-public-submission-v4.md) | 组织消费授权与本笔新输出 TXCer 的分离 |
| [用户自付 FUEL](docs/implementation-owner-fuel-v4.md) | 最终费用输入、用户找零/退款、无需组织费用授权 |
| [CAL 动态补资](docs/implementation-adaptive-reserve-v4.md) | 有限资金转入、原授权增量、成员水位控制与审计边界 |

设计文档保留版本演进；涉及当前行为时，应结合后续修订及对应实验报告阅读。

### 实验与复现

| 文档 | 主要问题 |
| :--- | :--- |
| [真实连续续花](docs/experiments/continuous-respending-2026-09-22/README.md) | 收到 TXCer 后能否继续付款，链长是否增加单跳负担 |
| [原 E2：组织代付](docs/experiments/finite-budget-2026-09-23/README.md) | 原代付配置的预算阻塞与费用审计，保留历史数据 |
| [E2：用户自付 FUEL](docs/experiments/owner-fuel-2026-09-23/README.md) | 用户费用充足、无组织代付时，CAL 与工作权限的周转 |
| [E2：CAL 动态补资](docs/experiments/adaptive-reserve-2026-09-23/README.md) | 有限补资能否支持恒定、阶跃及责任延迟下的并行周转 |
| [E3：赔付与历史修订](docs/experiments/liability-repair-2026-09-23/README.md) | 核心驱动与本地验证已完成；Mac 网络实验待运行，尚无正式结论 |
| [单组织整体 TPS](docs/experiments/single-org-tps-2026-09-22/README.md) | 快速签发、公共结算与成员收尾的完整系统吞吐 |
| [四委员共识容量](docs/experiments/consensus-capacity-2026-09-22/README.md) | 独立测量委员会工作点、过载边界及资源开销 |
| [完整路径优化](docs/experiments/full-path-opt-2026-09-22/README.md) | 成员内存模式、钱包保存及全流程阶段测量 |
| [第二轮 TPS 优化](docs/experiments/tps-opt2-2026-09-22/RESULTS.md) | 网关内存模式、有界并发及独立变量对照 |
| [数据库交互优化](docs/experiments/database-optimization-2026-09-20/README.md) | 跟块预处理、原子状态更新及存储开销 |
| [网关并行投递](docs/experiments/parallel-relay-2026-09-19/README.md) | 公共提交、INSTALL 与保存的并行关系 |
| [压测器发送与观察分离](docs/experiments/dispatch-lag-2026-09-20/PLAN.md) | 发送名额、后台未完成任务及测量口径 |

`main` 的连续续花链接指向 `re` 实验分支；切换到 `re` 后可直接访问实验代码、图表和原始数据。

## 实验配置

仅按需要启用以下选项。同条件对照应保持其他参数不变，完整组合以具体实验报告为准。

| 配置 | 用途 |
| :--- | :--- |
| `UTXO_EXPERIMENT_MEMBER_MEMORY=1` | 成员使用内存状态，保留原子更新与签票顺序 |
| `UTXO_EXPERIMENT_GATEWAY_MEMORY=1` | 网关使用内存状态，保留已完成状态检查与有界后台任务 |
| `UTXO_EXPERIMENT_COMMITTEE_MEMORY=1` | 委员会应用使用内存状态；需搭配 Comet 内存存储 |
| `UTXO_EXPERIMENT_MEM_BLOCKSTORE=1` | Comet 区块及相关状态使用实验 MemDB |
| `bench-v4 -wallet-no-sync` | 钱包不等待同步刷盘，仍执行验证与原子接收 |
| `UTXO_EXPERIMENT_COMMIT=250ms` | 显式调整高度推进时序；默认 500 ms，与执行过程重叠 |
| `UTXO_EXPERIMENT_FLUSH=10ms` | 调整发送刷新间隔 |
| `UTXO_EXPERIMENT_GOSSIP=10ms` | 调整交易传播间隔 |

成员默认 bbolt 模式已启用 NoSync，启动日志会显示 `storage_no_sync=true`。网关、钱包及委员会应用默认同步写入，除非显式选择相应实验选项。内存模式正常停机导出审计快照，拒绝将快照作为可恢复数据库重新打开。

### 压测与诊断

- **发送和观察分别限流：** `bench-v4 -concurrency 256 -max-pending 2048` 在钱包快速接收后释放发送名额；后台继续观察，总未完成任务受独立上限约束。报告保留发送滞后、未完成数量及最老等待时间。
- **单组织负载：** `bench-v4 -same-org` 在同一组织服务的两个钱包之间付款；成员进度默认批量查询，仍按各成员自己的逐笔状态判断完成。
- **缓存边界：** 已验证的完整收款描述符使用 1024 项有界缓存。逐笔付款授权、法定票数、输入消费与额度检查仍保留；热地址收益不能直接推广到大量新地址。
- **阶段诊断：** `UTXO_SETTLEMENT_TRACE=1` 启用委员会阶段记录。诊断会增加开销，应与正式性能轮分开；应用 Commit、钱包跟块观察和成员完成观察是不同时间点。

## 开发与验证

先应用 CometBFT 补丁，再执行测试和静态检查：

```sh
python3 third_party/cometbft/overlay.py

go test -tags=comet_v3 ./cmd/... ./crypto/... ./finality/... ./internal/... ./protocol/...
go vet -tags=comet_v3 ./cmd/... ./crypto/... ./finality/... ./internal/... ./protocol/...
go test -race -tags=comet_v3 ./cmd/payctl ./protocol ./internal/member ./internal/gateway ./internal/committee ./internal/store
```

这些命令明确限定源码目录，避免旧实验源码快照被当作业务包编译。实验私钥、数据库和构建产物不提交 Git；报告、公开配置、审计结果与复现脚本按实验目录保存。

<details>
<summary><strong>历史版本与早期实验</strong></summary>

以下材料保留用于追踪设计和性能演进，不应当作当前 wire 4 的运行说明。

- [v1.2 实现记录](docs/implementation-v1.2.md)：包含旧 wire 3 基线；对应提交 `039374d`。
- [早期共识阶段诊断](docs/experiments/latency-phase-2026-09-18/README.md)：传播间隔与提交阶段测量。
- [旧版中继重试实验](docs/experiments/relay-retry-2026-09-18/README.md)：保留 wire 3 的投递与证明处理对照。
- [早期后台减负实验](docs/performance/backend-reduction-2026-09-18/README.md)：逐笔证明路径下的批量处理结果。
- [成员 NoSync 修订](docs/experiments/member-nosync-default-2026-09-21/README.md)：实验存储默认值的变更与验证。

旧报告的计时起点、完成门槛、存储方式和工作负载可能不同，比较前需先对齐条件。

</details>
