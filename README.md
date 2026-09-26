<div align="center">

# Next

**基于担保组织的 UTXO 快速转账实验系统**

[![Go verification](https://github.com/19231224lhr/Next/actions/workflows/ci.yml/badge.svg?branch=re)](https://github.com/19231224lhr/Next/actions/workflows/ci.yml?query=branch%3Are)

**可续花预确认 · 直接担保责任 · 公共结算 · 可复现实验**

[实验总览](#实验总览) · [实验解读](#实验解读) · [性能基准](#性能基准) · [运行指南](https://github.com/19231224lhr/Next/blob/re/docs/operations.md) · [系统设计](https://github.com/19231224lhr/Next/blob/re/docs/design/system.md) · [文档导航](#系统与文档导航)

</div>

---

收款钱包验证 **TXCer** 后即可继续付款，无需等待前置交易完成公共结算。担保组织负责快速认证与直接担保，委员会负责公共账本、资金结算和异常赔付，钱包与组织成员自行跟块更新状态。

> **研究原型 · wire 4 · Go / CometBFT**
>
> **E1–E8 共八组实验已汇总到 `re` 分支**，包含实验代码、报告、图表与原始证据。`main` 保留早期性能基线代码，首页同步展示研究进展；复现实验请使用 [`re`](https://github.com/19231224lhr/Next/tree/re)。

## 实验总览

从“能否连续付款”到“异常时能否兑现”，再到多钱包负载下的资源成本，按下面的入口阅读实验。

| 实验 | 要回答的问题 | 已测结果摘要 | 报告与证据 |
| :--- | :--- | :--- | :--- |
| **E1 · 连续续花** | 未确认输出能否连续用于下一笔付款？ | 666 笔全部完成；100 跳快速可用中位 **198.630 ms** | [报告与图表](https://github.com/19231224lhr/Next/blob/re/docs/experiments/continuous-respending-2026-09-22/README.md) |
| **E2 · 资金周转** | 有限预算下，权限能否释放并继续签发？ | 三个动态补资场景各完成 **6,000 个两跳单位**；用户自付 FUEL | [动态补资](https://github.com/19231224lhr/Next/blob/re/docs/experiments/adaptive-reserve-2026-09-23/README.md) · [用户自付](https://github.com/19231224lhr/Next/blob/re/docs/experiments/owner-fuel-2026-09-23/README.md) |
| **E3 · 责任赔付** | 父交易缺失时，担保能否实际兑现？ | 正式对照 **108,000 笔付款、1,080 项唯一赔付**完成并通过审计 | [报告与图表](https://github.com/19231224lhr/Next/blob/re/docs/experiments/liability-repair-2026-09-23/README.md) |
| **E4 · 故障与冲突** | 单成员故障能否服务，冲突能否被阻止？ | 正式负载 **216,000 笔**全部完成；指定冲突用例通过 | [报告与图表](https://github.com/19231224lhr/Next/blob/re/docs/experiments/fault-conflict-2026-09-23/README.md) |
| **E5 · 交付门槛消融** | 后台 INSTALL 对快速交付有什么影响？ | 100 跳快速可用中位 **200.36 vs 287.98 ms** | [报告与图表](https://github.com/19231224lhr/Next/blob/re/docs/experiments/delivery-gate-2026-09-24/README.md) |
| **E6 · Lightning 参考** | 同机 LND 与本系统的实测延迟是什么？ | LND 收款 P50 **282.12 ms**；Next 补充轮 **0.963 ms**，配置与确认语义不同 | [报告与逐笔数据](https://github.com/19231224lhr/Next/blob/re/docs/experiments/lightning-2026-09-24/README.md) |
| **E7 · 跨组织与网络延迟** | 跨组织续花能否成立，通信变慢是否积压？ | **342,000 笔**正式付款全部完成；100 ms 跨站 RTT 下 ACK 约 **102 ms** | [报告与图表](https://github.com/19231224lhr/Next/blob/re/docs/experiments/cross-org-network-2026-09-24/README.md) |
| **E8 · 多钱包与资源** | 地址规模、真实续花如何影响性能和成本？ | **1,658,495 笔**正式已发付款全部完成；2048 钱包约 **494–499 TPS** | [报告与图表](https://github.com/19231224lhr/Next/blob/re/docs/experiments/workload-resource-2026-09-24/README.md) |

**阅读提示：** 各组的负载、存储模式和测量终点不同，不能直接拼成同一组性能结论。下文保留关键条件与边界，完整参数见各报告。

[全部实验索引](https://github.com/19231224lhr/Next/blob/re/docs/experiments/README.md) · [关键性能](#性能基准) · [结果与适用范围](https://github.com/19231224lhr/Next/blob/re/docs/experiments/README.md)

---

## 实验解读

实验围绕快速支付的四个问题组织：**收到后能否继续用、承诺能否兑现、为什么快、不同使用条件下表现如何。** E1–E4 检查业务机制及边界，E5–E6 提供内部对照与外部参考，E7–E8 检查组织互操作、网络影响和实际负载成本。

### E1 · 收到后立即续花

三个钱包依次转手，每次使用上一笔真实输出；链长为 1、10、100，分别对照“收到 TXCer 即续花”和“等待公共确认后再付款”，每种设置重复三次。

- **结果：** 18 个案例、666 笔全部完成；100 跳整链快速可用中位数为 **198.630 ms**，逐跳等待确认组为 **64.384 s**。
- **说明：** 快速凭证确实支持后继付款；固定单输入、单输出结构下，TXCer 为 941 字节，不随链长携带祖先历史。整链耗时包含中间构造与签名，不是单笔到账时间。

[E1 报告、图表与逐笔证据 →](https://github.com/19231224lhr/Next/blob/re/docs/experiments/continuous-respending-2026-09-22/README.md)

### E2 · 有限资金与签署权限周转

用真实未确认输入触发 CAL 责任占用，区分本金、费用和工作权限；在用户自付 FUEL 的条件下，对照固定预算与按成员可用权限水位进行的动态补资。

- **结果：** 恒定负载、阶跃负载、父提交延迟三个动态场景均从 **3,600 CAL** 启动，各完成 **6,000 个两跳单位、12,000 笔付款**，最终局部批准残余为零。
- **说明：** 在所测条件下，外部真实补资与责任核销能共同维持接纳。资金实际增加到 14,400 / 21,600 / 23,300 CAL，不能写成固定 3,600 CAL 无限周转；低额组受阻和工作权限边界仍保留。

[E2 动态补资主报告 →](https://github.com/19231224lhr/Next/blob/re/docs/experiments/adaptive-reserve-2026-09-23/README.md) · [用户自付对照](https://github.com/19231224lhr/Next/blob/re/docs/experiments/owner-fuel-2026-09-23/README.md) · [初始预算矩阵](https://github.com/19231224lhr/Next/blob/re/docs/experiments/finite-budget-2026-09-23/README.md)

### E3 · 前置交易缺失时兑现担保

主动扣住父交易，让后继付款先成立；对照父交易按时到达、赔付后迟到、永不到达，以及跨组织三跳，核查直接责任归属、扣款和受限历史输入修订。

- **结果：** 正式对照完成 **108,000 笔付款、1,080 项唯一赔付**，通过资金、费用、后继输出及四委员历史修订审计。
- **说明：** 所测缺失来源场景中，担保能落实为实际赔付，后继输出无需回滚。实际赔付是本金支出；永不到达的父交易仍可能留下自身未决工作占用。

[E3 报告、修复时间线与审计 →](https://github.com/19231224lhr/Next/blob/re/docs/experiments/liability-repair-2026-09-23/README.md)

### E4 · 成员故障与冲突付款

对单成员响应延迟、暂停及网关不同交付边界进行控制，并测试冲突消费、篡改和重复请求。

- **结果：** 正式九轮 **216,000 笔付款**全部完成；单成员受影响时，其他三成员仍在所测约 200 TPS 下完成签发；六类冲突检查未形成有效冲突 QC。
- **说明：** 结果支持指定故障下的可用性和冲突处理。也明确暴露边界：完整证据未扩散时网关暂停会阻断自主结算，并发冲突可能留下局部批准；进程暂停不等于掉电恢复。

[E4 报告、故障曲线与冲突明细 →](https://github.com/19231224lhr/Next/blob/re/docs/experiments/fault-conflict-2026-09-23/README.md)

### E5 · 提前交付究竟节省了什么

对照当前成证后立即交付的 A 组与增加后台完成门槛的 B 组，同时测试独立付款和真实 100 跳连续续花。

- **结果：** 相同实际 200 TPS 下，快速到账 P50 约 **1.30 vs 2.16 ms**；100 跳整链中位数为 **200.36 vs 287.98 ms**，提前交付组缩短约 **30.4%**。
- **说明：** 将后台等待移出收款关键路径，能减少到账与串行续花延迟。后台工作仍要完成；1000 TPS 档 B 组有未发任务，不能把该档直接解释为同负载的最大吞吐对比。

[E5 报告、交付窗口与后台待办 →](https://github.com/19231224lhr/Next/blob/re/docs/experiments/delivery-gate-2026-09-24/README.md)

### E6 · 与 Lightning Network 的同机延迟参考

在同一台 Mac Studio 上部署 LND 测量快速支付延迟，并补充 Next 的 100 TPS、3 秒输入，保留双方配置和逐笔记录。

- **结果：** LND 三轮共 300 笔成功，收款 P50/P95 为 **282.12 / 320.17 ms**；Next 补充轮 300 笔成功，快速 P50/P95 为 **0.963 / 1.208 ms**。
- **说明：** 这是指定配置下的实现级延迟参考。双方存储方式、负载和确认语义不同，不据此计算同等保障下的协议加速倍数，也未测量 Lightning 的 TPS 上限。

[E6 部署、计时定义与原始结果 →](https://github.com/19231224lhr/Next/blob/re/docs/experiments/lightning-2026-09-24/README.md)

### E7 · 跨组织付款与网络延迟

两个组织、两个独立钱包进程，在同组织／跨组织、独立输入／十跳续花之间对照；通过代理注入 0、20、100 ms 跨站 HTTP RTT。

- **结果：** 24 轮正式实验 **342,000 笔**全部完成；零注入十跳 ACK 平均 P50 为同组织 **1.615 ms**、跨组织 **1.633 ms**。100 ms RTT 的跨组织组 ACK 约 **102 ms**，三轮各五分钟持续承接实际 100 TPS，待办最后排空。
- **说明：** 支持双组织互操作、真实续花和指定网络延迟下的稳定工作点。ACK 包含返回付款方的时间；所有进程仍在同一物理机上，不是 WAN 共识或多组织线性扩容实验。

[E7 报告、网络曲线与首次付款对照 →](https://github.com/19231224lhr/Next/blob/re/docs/experiments/cross-org-network-2026-09-24/README.md) · [逐轮完整表](https://github.com/19231224lhr/Next/blob/re/docs/experiments/cross-org-network-2026-09-24/TABLES.md)

### E8 · 多钱包、真实续花与资源成本

对照 2 钱包独立付款、2048 钱包独立付款和 2048 钱包十跳续花；进行九轮五分钟主实验及三轮突增，统计延迟、CPU、HTTP 载荷、内存和历史数据增长。

- **结果：** **1,658,495 笔正式已发付款**全部完成；2048 钱包十跳组实际约 **494–499 TPS**，相对多钱包独立付款，CPU 约多 **20%**、HTTP 载荷约多 **21%**。
- **说明：** 展示身份规模和真实依赖付款的资源代价。保留尾段少发及墙钟计时限制；待办排空不等于历史内存停止增长，2048 钱包身份也不等于 2048 个独立客户端。

[E8 报告、资源曲线与突增结果 →](https://github.com/19231224lhr/Next/blob/re/docs/experiments/workload-resource-2026-09-24/README.md) · [逐轮完整表](https://github.com/19231224lhr/Next/blob/re/docs/experiments/workload-resource-2026-09-24/TABLES.md)

---

## 性能基准

下表是**已完成实验的工作点**，不是理论上限。原始数据、对照条件与复现脚本均在对应报告中。

| 场景 | 测量规模 | 实际结果 | 证据 |
| :--- | :--- | :--- | :--- |
| 单笔快速付款 | 三次独立单笔 | 中位 **2.017 ms**；三次为 1.749 / 2.017 / 2.274 ms | [单笔与续花数据](https://github.com/19231224lhr/Next/blob/re/docs/experiments/continuous-respending-2026-09-22/README.md) |
| 单组织完整系统，目标 2000 TPS | 每轮 15 万笔，重复两轮 | 完整闭环 **1949–1950 TPS**；到账 P50 **2.52–2.57 ms**，P95 **50.50–50.76 ms** | [整体 TPS 报告](https://github.com/19231224lhr/Next/blob/re/docs/experiments/single-org-tps-2026-09-22/README.md) |
| 单组织完整系统，目标 2200 TPS | 每轮 15 万笔，重复两轮；每轮约 71 秒 | 实际发送约 2127 TPS；完整闭环 **2104–2106 TPS**；到账 P50 **4.54–4.55 ms**，P95 **69.46–69.95 ms** | [整体 TPS 报告](https://github.com/19231224lhr/Next/blob/re/docs/experiments/single-org-tps-2026-09-22/README.md) |
| 四委员纯共识，目标 3000 TPS | 每轮 3 分钟、54 万笔，重复两轮 | 全部成功；含收尾实际吞吐约 **2986 TPS** | [共识容量报告](https://github.com/19231224lhr/Next/blob/re/docs/experiments/consensus-capacity-2026-09-22/README.md) |

上述 TPS 与 E1 数据采用其原有组织代付配置。新增用户自付路径已做 E2 功能及周转验证，尚未重新测量相同的最高 TPS。

完整系统的 2400 档触及在途任务上限，不作为稳定承接 2400 TPS 的证据。纯共识 3500 档单次通过，4000 档大样本未全部成功。

## 测量口径与实验环境

| 指标 | 起点与终点 |
| :--- | :--- |
| **快速到账** | 付款钱包开始 HTTP 发送 → 收款钱包验证 TXCer 与输出绑定、完成本地原子接收 |
| **完整闭环** | 包含快速到账、公共结算观察，以及各成员按确切交易事实完成本地收尾 |
| **纯共识 TPS** | 仅测委员会接纳和成功上链，不包含在线组织签发及钱包接收 |
| **整链耗时** | 第一笔发送 → 最后一跳对应终点；包含中间交易的构造、签名与等待 |

所有结果来自 **Mac Studio M4 Max（16 核、64 GB）同机多进程**环境。TPS 与 E1 性能轮采用组织及委员会应用内存状态、Comet MemDB 和钱包 bbolt NoSync，保留既有 Comet WAL/FilePV 同步；E3 使用委员会磁盘存储。每轮重新创世，不提供崩溃恢复保证；这些性能轮的钱包在同一压测进程内运行，不包含独立远端收款设备的网络交付时间。E7 另设双进程收款和 HTTP 延迟，其 ACK 时间包含回程。E2/E3 沿用预算驱动的发送阶段标记，包含少量 HTTP 前本地准备，具体口径以对应报告为准。

TPS 实验使用独立最终 UTXO、少量地址高复用；续花实验使用真实依赖输出。等待确认组还包含钱包确认观察、成员跟块及重试成本，因此表中差距属于本实现的两种付款方式对比。单笔延迟、单链速度和系统吞吐不能相互替代，也不外推为广域网、大规模地址轮换或无限期运行表现。

## 系统与文档导航

正常付款只需一轮成员取证：收款钱包验证新 TXCer 后即可继续付款；后台 INSTALL、公共提交和跟块独立推进。公共提交保留组织认证与所用输入的担保证据，不附带本笔新输出的 TXCer。委员会公开区块，钱包和成员自行跟块处理。

| 阅读目的 | 入口 |
| :--- | :--- |
| 了解完整流程、责任、资金与证明模型 | [系统设计与建模基线](https://github.com/19231224lhr/Next/blob/re/docs/design/system.md) |
| 对照关键代码、状态、并发和测试 | [工程架构与实现映射](https://github.com/19231224lhr/Next/blob/re/docs/design/architecture.md) |
| 构建、运行、诊断与测试 | [运行与验证指南](https://github.com/19231224lhr/Next/blob/re/docs/operations.md) |
| 查阅 E1–E8、性能数据与复现材料 | [实验总索引](https://github.com/19231224lhr/Next/blob/re/docs/experiments/README.md) |
| 查阅消息、费用、跟块与补资细节 | [现行实现参考](https://github.com/19231224lhr/Next/blob/re/docs/reference/README.md) |
| 准备论文模型与安全论证 | [安全证明文献调研](https://github.com/19231224lhr/Next/blob/re/docs/research/fast-payment-security-proof-survey-2026-09-25.md) |
| 查阅源码安全分析与已知问题 | [安全分析与证明准备](https://github.com/19231224lhr/Next/blob/re/docs/research/security-analysis-2026-09-26.md) |
| 追溯旧版规范与工程决策 | [历史归档与迁移表](https://github.com/19231224lhr/Next/blob/re/docs/archive/README.md) |

两份设计文档已按 wire 4 当前代码重新梳理，包含快速取证、直接缺口执行、权限释放、用户自付费用、赔付与历史修订的完整流程，并列出原子状态转移及代码／测试映射。假设和待证明性质已明确；正式形式化模型与安全证明仍是下一阶段工作，实验通过不替代证明。

首次运行请从[运行指南](https://github.com/19231224lhr/Next/blob/re/docs/operations.md#快速开始)开始；全部资料分类见[文档目录](https://github.com/19231224lhr/Next/blob/re/docs/README.md)。
