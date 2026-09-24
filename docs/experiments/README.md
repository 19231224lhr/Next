# 实验总览

本页汇总已完成的 E1–E8 共八组实验的问题、证据和结论范围。E2 的组织代付、用户自付与动态补资属于同一组实验的演进，不重复计为多组。吞吐优化记录是独立的基础性能证据。

E7 在 `e7-cross-org-network` 分支完成，基于既有实验集成版本增加双进程收款、站点 HTTP 延迟与双组织审计，不修改生产支付协议。

## 论文实验索引

| 编号与问题 | 主要证据 | 能支持的结论与范围 |
| :--- | :--- | :--- |
| **E1：到账后能否真实续花？** [报告](continuous-respending-2026-09-22/README.md) | 18 个案例、666 笔全部完成；324/324 次后继发送早于父交易提交；100 跳快速可用中位 198.630 ms | 所测最长 100 跳链可消费未确认输出并完成公共收尾；固定输入结构的凭证不携带增长的祖先链 |
| **E2：有限资金与权限如何周转？** [原矩阵](finite-budget-2026-09-23/README.md) · [用户自付](owner-fuel-2026-09-23/README.md) · [动态补资](adaptive-reserve-2026-09-23/README.md) | 三个动态 CAL 场景从 3,600 CAL 启动，各完成 6,000 个两跳单位、12,000 笔，最终局部批准残余为零 | 所测负载下真实外部补资与核销可维持周转；不是固定 3,600 CAL 永久支撑付款，低预算和低工作权限边界仍保留 |
| **E3：前置交易缺失时能否兑现担保？** [报告](liability-repair-2026-09-23/README.md) | 12 轮网络控制、9 轮缺失率对照；正式 108,000 笔、1,080 项唯一赔付完成 | 直接责任扣款、受限历史输入修订及账务审计在所测异常下有效；不是无限赔付或通用安全证明 |
| **E4：故障与冲突时能否正确服务？** [报告](fault-conflict-2026-09-23/README.md) | 216,000 笔正式付款完成；网关边界控制及六类冲突测试；未形成有效冲突 QC | 单成员响应延迟/暂停下继续签发，所测冲突未重复消费或收费；暂停保留状态，不代表掉电恢复，局部批准残留仍需计入 |
| **E5：快速交付门槛贡献了什么？** [报告](delivery-gate-2026-09-24/README.md) | 200 TPS 下 A/B P50 为 1.298–1.304 / 2.160–2.168 ms；100 跳整链中位 200.36 / 287.98 ms | 提前交付减少当前实现的到账等待与串行依赖延迟；后台工作继续进行，未发任务明确保留，不称为最大 TPS 对比 |
| **E6：与外部快速支付实现相比如何？** [报告](lightning-2026-09-24/README.md) | 同机 LND 三轮 300 笔成功，收款 P50/P95 为 282.12/320.17 ms；Next 100 TPS、3 秒、300 笔成功，快速 P50/P95 为 0.963/1.208 ms | 展示指定配置下的实现级观测延迟；负载、存储和确认终点不同，不计算同等保障下的协议加速比，不测 LN 容量 |
| **E7：跨组织与通信延迟的影响如何？** [报告](cross-org-network-2026-09-24/README.md) · [完整表](cross-org-network-2026-09-24/TABLES.md) | 24 轮、342,000 笔正式付款全部完成；100 ms 跨站 RTT 下三轮各持续五分钟承接实际 100 TPS，ACK P50 约 102 ms | 支持所测双组织互操作、因果续花与 HTTP 延迟敏感性；ACK 含回程，不证明 WAN 共识、最大吞吐或多组织线性扩容 |
| **E8：多钱包与续花负载的成本如何？** [报告](workload-resource-2026-09-24/README.md) · [完整表](workload-resource-2026-09-24/TABLES.md) | 9 轮五分钟主实验与 3 轮突增，1,658,495 笔正式已发付款均通过审计；续花相对多钱包独立付款约多 20% CPU、21% HTTP 载荷 | 支持固定共享客户端、内存实验模式下的负载敏感性与资源分析；保留尾段少发、历史增长及旧墙钟计量限制，不证明严格持续 500 TPS 或 2048 独立客户端扩展性 |

## 如何组合这些证据

E1 验证核心功能“未最终到账也能继续付款”；E2 检查支撑该功能的资金和权限约束；E3 检查承诺不能自然履行时的赔付；E4 观察故障及冲突边界；E5 用内部对照解释提前交付的收益；E6 提供成熟外部实现的延迟参考。六组共同构成当前研究原型的实验证据，不等同于完整安全证明或生产部署认证。

E8 补上真实多身份、多条依赖付款链与资源成本对照。E7 补充双组织与 HTTP 延迟对照，但两者仍沿用单机环境，不能代替真实多机扩展测试。新增单调计时短校验的快速 P50 为 1.105 ms，单列于 E8 报告，不覆盖旧九轮未保存的时钟信息。

| 补充性能证据 | 已测结果 |
| :--- | :--- |
| [单组织完整系统](single-org-tps-2026-09-22/README.md) | 目标 2000 TPS，两轮完整闭环约 1949–1950 TPS，快速 P50 2.52–2.57 ms |
| [四委员纯共识](consensus-capacity-2026-09-22/README.md) | 目标 3000 TPS，三分钟、54 万笔，重复两轮全部完成，含收尾约 2986 TPS |

## 统一阅读口径

- **快速到账**通常从付款钱包实际发送开始，到收款钱包验证 TXCer 并完成本地接收；E2/E3 的驱动标记包含少量发送前准备，以各报告为准。
- **公共确认观察**与**成员完成观察**包含跟块、验证及查询等待，不能当作共识内部 Commit 时间；完整闭环包括全部必要收尾。
- **单位、付款、责任项**不混算：一个两跳单位含两笔付款，一项赔付也不等于一笔新增用户付款；不同实验数据不累加成一个统一样本量。
- 每组采用自身固定的负载和配置。内存/NoSync 是显式实验选择，E3 保留委员会磁盘区块存储，LND 保留默认持久化；保留原始成功、失败、未发及残余记录。
- 实验均在 Mac Studio M4 Max 同机环境进行。三次重复与同一运行内的多笔付款不是同一种统计独立性；短时完成不外推为无限持续能力。

各报告链接原始记录、审计、配置和复现工具。README 只摘录关键结果，引用论文时应回到对应报告的计时定义与样本范围。

2026-09-24 与 [GPT 评审对话](https://chatgpt.com/c/6aa8b2d7-b6f8-83ec-8e23-ea9f3b634e90) 核对后，采用上述叙事顺序：E1–E4 为机制及适用边界的核心证据，E5 解释交付取舍，E6 单列外部参考。尤其保留 E2 低预算受阻、E4 局部批准残留和 E5 未发送任务；“完成实验”不等于所有配置承接全部计划请求。E6 两边同为 300 笔，也不代表相同负载或独立重复次数。

## 历史定位与优化记录

以下为较早阶段的工程诊断，保留原始记录，不作为上述正式实验的替代。

<details>
<summary>展开历史记录（保留原文）</summary>

- [Full fast-payment path at 500 and 600 TPS](full-path-opt-2026-09-22/FINAL.md): fresh all-source 90,000-payment runs at499.93/599.15 TPS, zero payment failures, equal committee states and drained outboxes; explicit member memory and wallet NoSync. 800 target not sustained. Reproduction tool and phase distributions included.

- [Admission to proposal: real mempool availability and broadcast](admission-wait-2026-09-21/README.md): 2,004 new audited payments plus offline analysis of the prior comparison. Both 1,000-payment traces select every transaction at its first actually available proposal opportunity; most delay is waiting for the previous height. A targeted broadcast trace locates 66.2 ms mean queue-to-receive delay in 124 payments that missed the preceding window. No production performance patch; three GPT review rounds and a narrowed follow-up plan.

- [Single payment versus 100 TPS for ten seconds](single-vs-100tps-2026-09-21/README.md): same journal-buffer candidate and full phase tracing; 1,003 audited payments including warmups. Fast receipt grows from 51.85 ms to mean 107.57 ms, mainly member persistence and shared benchmark-wallet saving. Additional backend latency concentrates in proposal waiting, consensus/signing-state persistence and subsequent-height header availability. All 1,000 load payments drain 0.912 s after the final send; no protocol changes or long-term throughput claim.

- [Bounded journal write coalescing candidate](journal-buffer-2026-09-21/README.md): 24,006 audited payments; fixed-rate 200/s ABBA lowers mean-of-run block-observation P50 by 23.3% and unfinished peaks by 28.8%, while total closure and final drain do not improve. A diagnostic pair reduces cumulative BlockStore write time with existing Sync boundaries preserved. Private candidate only; default binaries unchanged.

- [Actual Write/WriteAt calls and asynchronous persistence boundaries](write-calls-2026-09-21/README.md): 8,002 audited payments. The corrected trace attributes over 99% of the measured write-loop intervals to underlying write calls; bbolt emits 16 KiB pages and LevelDB mostly 32 KiB sequential journal writes. A small equal-data/equal-Sync ABBA file probe reduces calls twelvefold but improves total time only 4.3%, with more Sync waiting. No production optimization or durability relaxation.

- [Committee persistence and inter-block progression](committee-persistence-2026-09-21/README.md): 12,003 audited payments, three diagnostic runs. BlockStore synchronous batches grow from 25.0 to 90.2 ms per block in the paired trace; a further 200/s run attributes 59.7 of 76.8 ms to journal writing and 16.8 ms to Sync. bbolt spikes fall primarily in data-page write loops, not uniformly in Sync. Production behavior unchanged; lower-level I/O versus runtime causality remains open.

- [20 vs 200 TPS stage comparison](load-stage-2026-09-20/README.md): 16,013 audited payments including controls and warmups. In the diagnostic pair, admission-to-proposal grows by 110.88 ms and proposal-to-execution by 88.91 ms on average. Most extra proposal waiting precedes the previous height's app commit; a slow block records 640.47 ms in bbolt write/sync. Plain reverse-order controls retain the high-load tail/backlog trend, with variable magnitudes. No new production optimization.

- [Gateway handler persistence repair](gateway-handler-2026-09-20/README.md): bounded background saves remove the HTTP/1 connection dependency. At 200/s, four fresh 12,000-payment ABBA rounds show mean-of-run P50 154.72 → 113.88 ms; P95 279.82 → 249.20 ms. Background pending peaks and observation queries did not improve.
- [Foreground latency at 200/s](foreground-load-2026-09-20/README.md): preceding investigation, 52,026 audited payments including warmups; query-frequency ABBA gives no consistent tail improvement. Same-connection traces confirm response-afterwork blocks subsequent HTTP/1 requests; physical batch timing locates member and wallet waits in write/sync. Diagnostic overlays only in that investigation; the subsequent gateway fix is linked above.

- [Decoupled benchmark admission results](dispatch-lag-2026-09-20/RESULTS.md): 256 fast-receipt permits and 2048 total tasks; 24,000-payment candidate sends at 200/s with dispatch P95 0.575 ms. Background observation peaks at 1401 and fast P95 rises; the final coupled control retains 11 gateway 429 rejections. This corrects benchmark admission, not service-side throughput.

- [Paced sender lag investigation](dispatch-lag-2026-09-20/README.md): offline reconstruction of 54,000 existing samples. At 200/s, all 256 complete-lifecycle benchmark slots were occupied for 87.92% of the send window; capacity-conditioned residual dispatch wait P95 was 0.32 ms. No protocol changes or new load run.

- [Committee result batching and sustained load](committee-storage-2026-09-20/README.md): 70,207 audited payments; one synchronous batch replaces two independent result writes. Four 4,000-payment trials show a small 1.6% mean closure reduction. Two-minute 100/150/200 sends-per-second trials all drain; the 200/s trial shows growing sender lag, so no long-term 200 TPS claim. Tested version activated on the preserved laboratory data.

- [Consensus waiting, persistence and bandwidth trials](consensus-propagation-2026-09-20/README.md): 17,775 audited payments; proposer-local waiting and synchronous persistence traced. Shorter propagation intervals and a higher P2P bandwidth cap did not establish an end-to-end gain, so defaults remain unchanged. Adds optional bandwidth configuration and narrow diagnostic hooks.

- [Member GC and scheduling parameter trials](member-runtime-tuning-2026-09-20/README.md): 12,602 audited payments; lower member parallelism rejected, GOGC=200 adopted for the Mac lab. Repeated 100-payment comparisons and separate three-minute 30/s runs show lower foreground latency with a measured memory cost; no throughput-ceiling claim.

- [Fresh genesis, 100 payments and gateway dispatch](gateway-dispatch-2026-09-20/README.md): retired the active height-20840 runtime, 100/100 successful in 1.951 s at height 7; HTTP and selected-quorum timestamps, plus a separate 100-payment native runtime trace locating member-side GC and scheduling waits. Diagnostic changes only.

- [Foreground latency investigation](foreground-diagnosis-2026-09-20/README.md): unchanged binaries, 1,400 new audited payments; first-burst attribution and controlled send-rate comparisons. No production optimization added; a stable foreground penalty from WAL batching is not established.

- [Batch already-ready local proposals and parts](proposal-batch-2026-09-20/README.md): bounded WAL batching, 1,408 audited payments including a paused-committee check; six plain pairs show 4.8% shorter closure, with a measured foreground-latency regression retained in the report.

- [Skip redundant WAL synchronization](wal-sync-2026-09-20/README.md): minimal AutoFile change, 800 audited payments, six alternating plain runs and two operation profiles; required durability preserved.

- [Consensus and block-following review](consensus-review-2026-09-20/README.md): two GPT review rounds, current-binary operation profiling, actual remaining timers, header availability and narrow WAL optimization candidates.
- [Current version: 100-payment phase retest](stage-profile-100-2026-09-20/README.md): identical traced binary, full phase timings, per-block consensus breakdown, send window and drain comparison.
- [Database optimization and controlled comparisons](database-optimization-2026-09-20/README.md): four changes, 9,500 performance-test payments, real repair/restart validation, intermediate regressions and separate fresh/grown-database results.

These are single-host diagnostic samples from the Mac Studio prototype, not
production or sustained-throughput claims. Raw reports retain phase timestamps,
delivery-attempt identities and limitations. No laboratory keys or databases are
included. See the project README for the laboratory commands and timing scopes.

- single-transfer-repeat.json: three isolated finalized-UTXO payments after moving gateway persistence off the response path.
- single-transfer-settlement.json / .md: one payment traced through backend delivery and committee commitment.
- single-transfer-async.md: initial trace after the gateway response change.
- backend-review-2026-09-18.md: two-round review, confirmed delivery amplification, and the minimal measurement/fix sequence; no performance fix is claimed yet.

The backend trace observed 28 delivery attempts for one immutable payment.
Its longest measured interval was committee admission to block execution;
individual Comet consensus phases had not yet been instrumented.

- [35-payment phase diagnosis](latency-phase-2026-09-18/README.md): reversible propagation-parameter experiments, raw four-node traces and scripts. Defaults and delivery logic remain unchanged.

- [Fresh-genesis retry repair](relay-retry-2026-09-18/README.md): tracing-off paired runs, stable retry envelopes, offline financial audit, and wallet restart/drain.

</details>
