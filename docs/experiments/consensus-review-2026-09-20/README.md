# 共识与跟块：两轮评审及补充诊断

2026-09-20，代码 `6aa0d71`。已将实际代码、上一轮 100 笔全阶段时间及本次补充诊断，分两轮发给 [GPT 评审会话](https://chatgpt.com/c/6aa8b2d7-b6f8-83ec-8e23-ea9f3b634e90)。完整往返见 [gpt-review.json](gpt-review.json)。GPT 根据提供的代码和数据评审，没有独立运行本仓库。

**推荐：优先确认并消除“已经同步、没有新增数据”的 WAL 重复同步；其次验证一个本地提案及其片段能否有限合批。当前没有已证明安全、只改一行就能大幅提速的方案。超时和跟块轮询暂不改。**

## 本次验证

- 核对客户端取块、结果验证、跟块循环、委员会配置，以及实际 Comet 分支的提案、投票、WAL、FilePV、BlockStore、结果保存路径。
- 四程序 SHA256 与上一轮最终版完全相同，仅启用已有 `UTXO_COMET_PROFILE=1`，新数据库中再发 100 笔最终 UTXO 跨组织交易、64 闭环并发，全部验签和同步保留。
- 100 笔全部成功，四委员状态相同，14 节点 outbox 排空，CAL/FUEL/费用匹配、资金缺口为零。原空闲实验网已恢复。
- 本轮完整闭环 **1.981539 秒**，原始快速到账 P50 **128.233 ms**、P95 **154.544 ms**。这是相同代码的一次诊断样本，埋点级别也不同；不能把相对 2.132 秒的变化算成优化收益，也不能称为稳定低于 2 秒。
- 本轮没有修改生产代码、同步强度、共识参数或跟块认证规则。

## 同步成本已定位到实际调用

下表是本次 committee0 在四个业务块上的逐调用中位数，不是单笔付款耗时。不同节点并行，嵌套项不可重复相加。

| 操作 | 调用数 | P50 |
|---|---:|---:|
| 投票前 WAL FlushAndSync | 8 | 16.635 ms |
| FilePV 签署状态保存 | 9 | 16.181 ms |
| 完整投票签署调用，包含 FilePV 保存 | 8 | 17.196 ms |
| 本地消息写入并同步 WAL | 10 | 12.708 ms |
| BlockStore 保存 | 4 | 19.782 ms |
| EndHeight WAL 写入并同步 | 4 | 17.549 ms |

“投票阶段几十毫秒”不能理解为纯签名计算。签名调用包含防双签状态保存，前后还有 WAL 同步；这些职责不同，不能直接当成重复内容删除。

入口收到→真正提案者完成 mempool 检查的 P50 为 **16.955 ms**；提案者已检查→PrepareProposal 为 **195.258 ms**。主要提案等待已发生在提案者本地，不是组织到委员会 HTTP 慢。

本轮高度 3 的提案者 committee1 生成 63 笔交易块，PartSet 为 3 个片段。自身 `signed proposal` 到 `received complete proposal block` 之间有四次 `internal_message_wal`：**9.601、7.055、9.814、20.953 ms，原始精度合计 47.423 ms**。源代码对应一条 ProposalMessage 和三条 BlockPartMessage，每条均 `WriteSync → handleMsg`。

这是有限合批的具体机会：尝试将这四次同步变成一次。但不能宣称会省掉全部 47.423 ms，保留的一次同步及传播重叠仍有成本。另两次本地投票消息同步不属于该合批范围。

## 两个方向暂时降级

### TimeoutCommit 不是提交完成后再睡 100 ms

代码为 `StartTime = CommitTime + 100ms`。上一轮 2.132 秒实验中，committee0 新高度 3/4/5/6/7 的实际剩余定时分别为 **-23.612、-52.137、-1.823、-0.801、-43.070 ms**，其他节点大多也已经到期。个别相关高度仍余约 0.3～11 ms。高度 2 曾余 41～51 ms，但在负载发送前的启动空闲阶段。

原生 `SkipTimeoutCommit` 在收齐全部 precommit 时可以提前推进，但本轮主要高度没有 100 ms 空转可省。因此先不改它，也不直接调小 TimeoutCommit。

### 跟块主要在等待后继头形成

`VerifyBlock` 使用 H+1 签名头中的 `LastResultsHash` 认证 H 的执行结果。H 的 commit 本身不认证 H 执行后才生成的结果，不能以未经认证的 BlockResults 替代。RPC 已经使用最新高度的 `LoadSeenCommit`，不再等待 H+1 应用提交或 H+2。

客户端固定读取 committee0。本次逐业务块对齐结果：

| H | Commit(H)→BlockStore 保存 H+1 返回 | 之后→钱包收到 H+1 头 |
|---|---:|---:|
| 2 | 267.011 ms | 0.351 ms |
| 3 | 212.990 ms | 0.273 ms |
| 5 | 285.024 ms | 0.344 ms |
| 6 | 237.005 ms | 3.346 ms |

保存函数返回是接近可用时点的标记，不是专门测得的 RPC 发布时刻。但可见本轮拉取尾段很小，缩短 25 ms 轮询或并行 GET 无法消除前面的两百多毫秒。

## 最小后续方案

1. **先只计数，不跳过 Sync。**在 WAL 同一把锁内记录文件代际、写入序号、上次成功同步序号、新增字节、调用来源及高度／轮次，区分关键路径与空闲同步。当前埋点只有耗时，还不知道“没有新增内容却再次同步”的命中率。
2. **命中值得优化才做小补丁。**只有能证明自上次成功同步后没有新增内容时才省略这次同步。不能只看 `Buffered()==0`：bufio 可能已自动写出但尚未同步。部分写入、失败及轮转不得被误判；只有成功同步才能推进持久位置。如果关键路径命中很少，则结束该方向。
3. **必要时再做同一提案的有限合批。**只合并已经就绪、同高度／轮次／提案的 Proposal 和 Parts，设条数和字节上限，不等新消息凑批；Vote、Timeout、EndHeight 不纳入。`handleMsg` 可能产生 `newStep` 等 WAL 记录，批量预写可能改变原有交错，因此不能仅凭格式不变就认为重放正确。

合批必须验证同步失败不处理消息，同步后处理到中途、最后片段触发投票等时点的崩溃重放一致性，以及远端消息／超时交错。如果需要明显改造恢复机制才能成立，则后置。

每次只选一个补丁做同条件 A/B/A：主要看实际同步次数、本地提案完成时间、块周期和末尾 H+1 头可用时间，再看快速到账尾延迟、唯一业务完成速率及在途数量。不能凭一次总秒数决定是否采用。

FilePV、BlockPartSize、Finalize 响应存储和跟块认证先保持现状。`SaveFinalizeBlockResponse` 的 Set＋SetSync 仅有一次明确同步，改 Batch 不等于自动省掉一次 fsync。

## 证据与复算

- [diagnosis.json](diagnosis.json)、[diagnose.py](diagnose.py)：实际定时、后继头等待和四次提案消息同步的复算。
- [profile-summary.json](profile-summary.json)：四委员真实操作耗时和提案者就绪时间。
- [stage-summary.json](stage-summary.json)、[transactions.csv](transactions.csv)、`reports/trace100`：本轮全部阶段和原始时间线。
- [sources.md](sources.md)：实际跟块、执行结果认证和应用提交代码摘录。
- [gpt-review.json](gpt-review.json)：两轮完整讨论；[metadata.json](metadata.json)：版本及二进制指纹。
- [run_profile.py](run_profile.py)：复用上一轮运行器，仅增加诊断开关；独立目录、保留旧实验。

复算：先运行既有 `parallel-relay-2026-09-19/analyze.py <本目录>` 和 `analyze_profile.py <本目录>`，再运行 `python diagnose.py`。
