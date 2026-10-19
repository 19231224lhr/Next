# C3 性能优化与真实服务比较

最终保留 Stage 9b，Stage 10 因三轮退化已撤回。使用最终四个二进制和两个 C3 arm 相同的新客户端，**两物理核预算下 100 笔链比调优 FastPay 的总链中位数低约 26.3%，四物理核预算下 1000 笔链低约 23.36%**。但两物理核的 1000 笔结果应描述为接近持平、存在批次波动；长链接收 P95 没有建立全面优势。

最终三组串链确认、五对混合负载、九次费用观察检查及全仓/race 验证均已完成。完整阶段历史、pilot、拒绝建议和不利结果保留在 [DECISIONS.md](DECISIONS.md)，不会因最终版本选择而删除。

## 最终串链结果

单位为 ms。先对每个 run 计算指标，再对各 run 的指标取中位数；P95 按 `(n-1)*p` 线性插值。所有下列 run 均 complete；C3 完成 public/member 闭合，FastPay 完成接收验证、继续转账探针与本金守恒检查。三个 arm 顺序执行并轮换顺序，正式比较不开 trace/profile。

| CPU 预算 / 工作量 | 每 arm 轮数 | 冻结服务 C3 总链 | 最终 C3 总链 | 调优 FastPay 总链 | C3 对冻结 / FastPay 配对胜数 | C3 / FastPay 接收 P95 |
|---|---:|---:|---:|---:|---|---|
| 0–3：2 物理核、4 SMT / 100 笔 | 9 | 713.488 | **471.374** | 639.286 | **9/9、9/9** | **5.990 / 6.917** |
| 0–3：2 物理核、4 SMT / 1000 笔 | 7 | 10354.352 | **6349.661** | 6611.357 | **7/7、6/7** | **11.077 / 7.348** |
| 0–7：4 物理核、8 SMT / 1000 笔 | 7 | 10023.665 | **5236.536** | 6832.863 | **7/7、7/7** | **7.644 / 7.618** |

对应实际目录为：

三行正式结果另由原始 summary 生成 [final-results.csv](final-results.csv)，便于直接复核。

- `final-build-chain100-9triples`：最终 C3 范围 **453.840–505.161**，FastPay **630.347–696.873**；接收 P50 为 **4.074 / 6.281**。
- `final-build-chain1000-7triples`：最终 C3 范围 **6280.492–6627.331**，FastPay **6494.771–6901.882**；接收 P50 为 **4.563 / 6.448**。第 1 轮（零起点）C3 **6627.331**、FastPay **6542.545**，保留这一负配对。
- `final-eightcpu-chain1000-7triples`：最终 C3 范围 **4971.495–5264.484**，FastPay **6564.359–6947.405**；接收 P50 为 **3.693 / 6.732**。P95 接近相同，不能把总链优势写成全面尾延迟优势。

两核长链最新中位数比 FastPay 低约 **3.96%**，但旧共同客户端组 `stage9b-long-confirm7triples` 的 C3 中位数 **6483.819**、FastPay **6412.225**，C3 反而慢约 1.1%。旧组 P95 **10.939 / 6.937** 也不利。综合这些记录，只能说两核长链接近持平并有批次波动，不能宣称稳定普遍领先。

旧 Stage 9b 测量组共享 pre-9b 客户端；新 final 组两个 C3 arm 使用同一个新 payctl，冻结 member/gateway/committee 不变。两组不池化，也不把差值解释成“仅重编译”或单个补丁的收益。四核组是独立 CPU 预算和运行批次，不能用跨预算差值作纯核心数因果归因，也不能替换两核的不利结果。

## 配置公平性与端点

冻结服务二进制来自代码版本 **`a9d943e`**，位于 `/home/qyinf/next-c3-20261013/current-bin`；**`7e98a27` 是交付 HEAD**。最终 raw binaries 位于 `final-bin`，选定 wrapper 为 `final-compact-bin`，共同新客户端的冻结服务 wrapper 为 `frozen-final-client-bin`。各 plan/variant 记录真实源代码、二进制 SHA-256 与额外 CLI options；compact 是累计配置收益的一部分。

两个 C3 arm 都用 **GOMAXPROCS=1**；调优 FastPay 四个 authority 和 adapter 都用 **Tokio worker=1**。FastPay 官方源码固定于 `db39bb0cddc94b8c34dc235b1cfdae4c1285c6f2`。隔离敏感性版本仅增加两个 runtime constructor 的可选 worker 设置；环境 unset 时保留官方 constructor，官方源码/二进制/结果单独保存。

实际环境为 **AMD Ryzen AI 9 HX 370、Go 1.27.1、Ubuntu/WSL，kernel 6.6.87.2**。本文“2/4 物理核、4/8 SMT”按 **WSL guest topology** 描述 affinity 映射；taskset 约束 guest 内本轮进程，不表示独占宿主物理核心或屏蔽宿主其他活动。

双方都做运行时选择。四核 `fastpay-eightcpu-long-runtime3` 有 **15 个 verified run、每设置 3 个**：official / unset / 1 / 2 / 4 总链中位数 **7116.751 / 7083.715 / 6740.121 / 7049.472 / 6987.550**，选 1。相同最终 C3 代码的 `final-eightcpu-gomax-pilot3pairs` 比较 GOMAX1→4，中位数 **5313.514→5399.264**、2/3 对退化，保留 1。选择 pilot 与之后独立七轮系统比较分开。观察到的 OS thread 数包含主线程及运行时支持线程，不是精确 Tokio worker 数。

链/混合 C3 运行四个 member、一个 gateway、四个 committee validator；两组织费用顺序回归运行八个 member、两个 gateway。CPU 集由所有进程共享；GOMAXPROCS 是每进程运行时控制，不等于总 CPU 配额，也不与 Tokio 调度模型直接等同。

串链在三钱包间循环 100 单位本金，下一笔依赖上笔的接收完成。快速接收端点保留证书、输出绑定和接收钱包提交；public/member 闭合另行记录。FastPay receiver-confirmed adapter 保留资金检查，调用官方 `receive_from_fastpay` 验证并同步 quorum，核对交易身份和余额；探针在计时后进行。总链从第一实际发送/RPC 到最后接收，后续构造、签名、排队和后台竞争计入串链间隔；单笔 receipt 与含构造的 request-to-receipt 指标不混用。

所有服务保持原实验内存状态配置，C3 钱包为 ext4/NoSync，FastPay wallet 为内存；这不是统一持久化存储栈、WAN 或生产崩溃持久性测试。初始化不计时，复用的只是不启动的 pre-service fixture，各 run 的运行状态仍 fresh，不能复用已运行的数据库。

## 实现与剩余瓶颈

保留的优化均减少等价重复工作：父输出验证 token；初始认证/资金验证与编码合并；精确字节、512 项/16 MiB Outbox cache；member Approval JSON 移出 writer 但预留/冲突/写入仍原子；Collector 不变 summary 模板复用；同一已验证字节快照共享 INSTALL；只读快照与按 action admission 延迟分配 preparation；仅因容量遗漏工作时重开扫描；同一 C3 public event 复用已读自身 Approval，并保持实际 fact 绑定与不同 fact 的第二 key 读取。

没有改变 signed format、经济规则、QC 门槛、CAL/FUEL 原子预留或 receipt 时点，也没有关闭 settlement/recovery 或放松存储设置。Stage 10 的跨调度 preparation/submission cache 三轮都退化，中位数 **6449.900→6566.363**，已撤回；补丁和 pilot 保留在 `stage10-candidate/stage10-only.patch`、`stage10-long-pilot3triples`。

同样拒绝了会忽略已知字段类型错误的 Recovered-only wallet JSON projection、无必要的最终 QC 跳检/大型可信 accumulator，以及尚无主导因果证据的第四 HTTP 请求 detach。没有用微基准或 CPU 节省代替端到端确认。

最终两组长链的精确逐 run 时间轴残差均低于 0.01 ms；下面各列是独立中位数，不可横向相加：

| CPU 预算 | C3 / FastPay receipt 总和（ms） | 后续 request build（ms） | 最慢十笔 receipt 总和（ms） |
|---|---|---|---|
| 两物理核 | **5760.75 / 6519.56** | **577.46 / 90.78** | **503.71 / 109.57** |
| 四物理核 | **4669.06 / 6742.02** | **547.46 / 90.80** | **439.06 / 95.93** |

两组 C3 各七轮、两个 C3 arm 的每个 run 都确认 1000 个 public 输出与 1000 个 member closure，重试 hop 和 progress error 均为 0。最终 C3 从快速链结束到全部 member 闭合的额外 drain 中位数为两核 **1170.91 ms**、四核 **1151.56 ms**；observer peak 中位数分别为 **310 / 388**。这证明后台闭合仍实际完成，也说明快速接收结束不等于背景已全部消化。检查范围是保留的 wallet public 观察和 recorded member closure，没有新增 store-key 或完整经济审计。

已有 Stage 8/9 诊断中，transaction generation 约占 preparation wall time 25%、SaveDirectRequestBytes 约 50%、签名不足 7%；queue 平均占比不大但有 15 ms 尖峰。请求准备与收证长尾仍是两条独立证据链，详情见 [timing-breakdown.md](timing-breakdown.md)，不承诺单改某项即可普遍领先。

## 混合负载与费用回归

最终 `final-build-mixed5pairs` 共十个 run，全部 complete。CPU 0–3、100/s、30 s 名义 3000 时隙下，冻结→最终 C3 的 P50 **9.758→2.988 ms**，P95 **39.413→21.961 ms**，**5/5 对两指标均改善**。attempted=receipts=closed 中位数 **2947→2962**，范围 **2942–2951→2960–2964**；实际提交完成率 **1.0**。receipt goodput **98.2→98.7/s**，public goodput **94.333→95.433/s**。

runner 有意跳过 missed pacing slots，因此实际发出者全部完成不等于名义 3000 全部发出。这是固定 offered-load 回归，不是最大吞吐量或与 FastPay 的相同混合模型比较。前组 `final-stage9b-mixed5pairs` 的 7.983→2.846 ms P50、35.842→18.963 ms P95 和原始样本继续保留，未合并或静默替代为 final 二进制组的数字。复用仅为未启动 fixture 的初始化副本，每个 run 的执行状态仍 fresh。

费用三轮 × source-first/child-first/compensated，共九个原生 run，修订检查 `final-fee-verification-v2` 为 **PASS 9/9**：同轮 TxID 和 owner-signed fixed section 相同，全部 pairs closed；Source/Child 的用户费均 10、Rewards 均 2、Held 均 0，issuer pool 收支与资金守恒核对通过。

| 顺序 | Source Burned / Released | Child Burned / Released |
|---|---|---|
| source-first | **0 / 4** | **0 / 4** |
| child-first | **2 / 2** | **0 / 4** |
| compensated | **4 / 0** | **0 / 4** |

compensation-before-source 中间观察为 Source Burned=3、Rewards=2、Held=1、Released=0、尚未收取 quote。初版检查器错误要求 `ReplayError` 空，把生产者记录非成功重复请求的语义误作经济失败；误判结果保留在 `final-fee-verification`。实际九次 replay 为 HTTP 503 / `http-rejected`，完整 escrow/pool snapshots 不变。v2 证明的是 HTTP 明确拒绝与观测状态不变，**不是 ledger inclusion 或执行层幂等的证明**；协议代码/测试没有为修订检查改变。

## 验证与可复现证据

[final-verification/status.json](final-verification/status.json) 记录 tagged 全仓测试、protocol/rules/member/gateway/transport tagged race 和四个 Linux build 全部 exit 0；[manifest.json](final-verification/manifest.json) 保存 `all_steps_passed=true`、source/binary SHA-256。隔离 FastPay release 测试及实际 invalid runtime 参数检查通过，原配置字节不变。CLI guard 的 package 测试、两个 runner 各 0/1001 的四次拒绝且无输出目录创建，以及 AST 解析已有 [harness-boundaries.json](final-verification/harness-boundaries.json) 记录。未把 AST 解析冒称 `py_compile`。

Linux 原始根目录为 `/home/qyinf/next-c3-speed-20261014`；表中 dataset 名就是该根目录的实际相对目录，每组保留 plan/status、日志、manifest/result、原生 samples、closure/probe。历史 Stage 6 短链 22.1%、前组 18.65%、两核旧长链慢 1.1%、拒绝候选与 runtime 退化仍全部保存，不与 final 组池化。

最终七项报告分析均 exit 0，记录在 `raw-results/final-analysis-status.json`；两组长链目录还保留 `timing-breakdown.json`、`background-check.json` 和 `bands.csv`。Windows 原始结果入口为本目录的 [raw-results](raw-results)，逐文件 SHA-256 与 dataset 状态以 `export-manifest.json` 为准；代码入口为 [final-code-delivery](final-code-delivery)，按 patch/source/package manifest 核对。导出排除 keys/config/state/db/genesis、二进制和大 binary profile。复现命令及存储/fixture 边界见 [REPRODUCE.md](REPRODUCE.md)。

导出记录为 **6482 个文件、1,085,792,052 bytes**，全部逐文件 SHA-256 核对正确，无缺失、无 source_changed，见 [export-integrity.json](final-verification/export-integrity.json)。最终 source-integrity 检查中 **23 个变更 Go 源文件 SHA-256 全部与验证构建记录一致**，见 [source-integrity-final.json](final-verification/source-integrity-final.json)。
