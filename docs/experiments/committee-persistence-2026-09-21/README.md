# 委员会持久化与块间推进：2026-09-21

本轮调查实际写入与同步尖峰，不修改生产协议、数据库同步规则或共识参数。诊断代码通过私有依赖副本、独立 modfile 和 Go overlay 编译；根 go.mod、模块缓存及生产二进制不变。

## 第一组证据：20/s 与 200/s 各 4,000 笔

Mac Studio 单机运行四委员、两组织八成员、两网关；每轮独立创世，48,002 个初始输出。先 200/s，后 20/s。每轮另有一笔预热，快速到账并发 256，总未完成上限 2048；所有验签与同步持久化保留。使用完整有界节点时间线和约五分之一钱包抽样，测量结束后导出。

两轮均 4,000/4,000 成功，未命中发送或总任务上限；包括预热共 8,002 笔。四委员状态在各轮内一致，14 节点 pending 均为零，CAL/FUEL/费用审计通过。200/s 的进度查询有 364 次错误，均未导致最终任务失败；这些错误不能省略为“所有 RPC 无错误”。

以下按每个委员会副本的业务块统计：20/s 为 502 块 × 4，200/s 为 31 块 × 4。同一块四副本有关联；两组块大小不同，不是同写入量的磁盘竞争实验。数字为平均墙钟毫秒。

| 操作 | 20/s | 200/s |
|---|---:|---:|
| 应用 App.Commit | 32.687 | 45.490 |
| bbolt 原生 WriteTime | 32.445 | 43.542 |
| bbolt 数据页 writeAt 循环 | 0.164 | 11.322 |
| bbolt 数据同步 | 18.259 | 21.113 |
| bbolt meta 同步 | 13.983 | 10.981 |
| 两次同步合计 | 32.242 | 32.094 |
| Comet BlockStore 保存 | 25.014 | 90.253 |
| 其中 batch.WriteSync | 24.989 | 90.153 |
| EndHeight WAL | 26.180 | 22.877 |
| Finalize 响应保存 | 32.091 | 26.543 |
| 共识状态保存 | 18.349 | 24.474 |

**高负载增量不能概括成“bbolt 的 Sync 全面变慢”。** 两次同步的合计均值几乎没变，数据页写入循环出现了重尾。与此同时，BlockStore 同步批次的增加比应用提交更突出。表内存在父子操作，不能把整表相加。

200/s 的 h25 有 149 笔付款：

| 节点 | App.Commit | 数据页写循环 | data Sync | meta Sync | spill | grow |
|---|---:|---:|---:|---:|---:|---:|
| committee0 | 296.951 | 226.724 | 58.432 | 9.920 | 0.853 | 0.006 |
| committee1 | 296.302 | 255.429 | 27.721 | 9.880 | 1.383 | 0.007 |

这两个尖峰主要落在写入循环，而不是写锁、spill 或文件扩容。循环墙钟仍可能含 GC/调度和系统调用等待，不能直接叫“磁盘设备耗时”。本轮没有重现上一轮 642 ms 的相同最大值，不能将两轮拼成同一笔事件。

按付款加权，接纳至提案平均 289.116 → 448.596 ms；其中发生在实际提案者前一高度 App.Commit 完成以前的部分为 270.410 → 424.103 ms，之后为 18.706 → 24.493 ms。前一高度未完成包含该高度的共识和其他持久化，**不是全部都在等待应用数据库**。所有轮次均为 round 0。

提案至 Finalize 平均 316.817 → 570.155 ms；其中 `finalizing commit` 日志至 Finalize 为 51.143 → 124.621 ms，与该位置的 BlockStore + EndHeight WAL 相符。高负载发送窗口约 20 s，含后台观察的总实验为 20.734 s；低负载为 200.403 s。增加诊断与单轮波动会影响数值，不把这些数字与之前关闭诊断的轮次直接作性能优劣结论。

## 追加短测：BlockStore 内部到底慢在哪

第二个私有诊断版本只增加 GoLevelDB 内部计时，再以 200/s 发送 4,000 笔，另有一笔预热。全部完成并排空；34 个业务块、四委员，共 136 个同步批次全部匹配，没有为凑结果丢弃慢样本。本轮独立统计，不与第一组拼接成一次实验。

| batch.WriteSync 内部阶段 | 平均 ms | P95 ms |
|---|---:|---:|
| 整个同步批次 | 76.799 | 138.544 |
| 等写者锁 | 0.000325 | 0.000500 |
| 前台 flush / memtable 容量处理 | 0.249 | 0.001 |
| journal 组装、写入及 Flush（不含 Sync） | 59.686 | 119.210 |
| journalWriter.Sync | 16.835 | 26.855 |
| memtable 应用 | 0.0146 | 0.0316 |
| 其他剩余 | 0.0154 | 0.0269 |

均值高于 P95 的 flush 项由少量长事件拉高，最大为 15.482 ms，并非计算错误。最慢的 committee2/h20 批次为 188.580 ms，其中 journal 写入段 167.840 ms、Sync 20.689 ms、flush 0.0013 ms。

**此处的首要热点已经收窄到日志写入段，不是写者锁排队，也不是 memtable 应用。** 本轮样本没有走大批次特殊事务或合并等待路径；该判断来自每个父批次内部唯一的 write/locked/各阶段调用，以及完整残余校验。日志段仍含 CRC、复制、文件 Write 和运行时等待，尚不能把它全部归为磁盘或 CPU 时间。

同一追加轮还出现 bbolt 尖峰：h31、159 笔付款，committee0 App.Commit 为 881.932 ms，数据页循环 839.139 ms、两次同步合计 27.733 ms、spill 0.841 ms、grow 13.104 ms。committee3 同块数据页循环为 822.181 ms。尖峰发生在两个不同的数据库写入路径；时间接近不证明两者共同的底层原因。

LevelDB journal 源码使用 32 KiB 缓冲，`writeBlock`/`writePending` 调用底层 `Write`，一次业务 batch 最后再 Sync，**不是每个片段都 Sync**。减少同一批次内的小 Write 是候选，但当前还没有逐次 Write 的计数、字节和耗时证据，不能声称这个改动已经必然有效。

## 源码中的实际顺序

1. `internal/committee/app.go: App.Commit` 将整块 `pendingChanges`、meta 等合并为一次 `db.Update`，并非每笔一次数据库提交。
2. `internal/store/bolt.go: Bolt.Update` 使用单写者；`bbolt/tx.go: Tx.Commit` 先 spill/grow，再写数据页并同步，再写 meta 并同步。原生 WriteTime 不含前面的 grow，也不是单次 fsync。
3. `Comet consensus/state.go: finalizeCommit` 先 `BlockStore.SaveBlock`，再写 EndHeight WAL，然后调用区块执行。这是已有重放顺序。
4. `Comet state/execution.go: applyBlock` 在 Finalize 后可靠保存响应，再 App.Commit，再保存共识状态，最后推进下一高度。响应的两项记录已经合批，不是本轮新优化机会。
5. 实际 Comet backend 是 GoLevelDB。`cometbft-db/goleveldb_batch.go` 的 `WriteSync` 调用 LevelDB `Write(...Sync:true)`；后者包含写者等待、memtable flush/compaction 等候、日志写入/同步及内存表应用。因此 BlockStore 的 90 ms 不能直接称为一次 fsync。

实验委员会数据库的 meta 实测页大小为 16,384 字节。当前 Go 的 Darwin `File.Sync` 使用 `F_FULLFSYNC`，不支持时才回退。该事实说明平台同步语义，**不证明尖峰由 SSD 硬件或某一个系统调用造成**。PageAlloc 是页分配统计，不是硬件实际写入字节；Write 计数不是同步次数。

## 复核与下一步

已将真实代码路径和三轮数据发到既有 [ChatGPT 对话](https://chatgpt.com/c/6aa8b2d7-b6f8-83ec-8e23-ea9f3b634e90)进行多轮评审。

现阶段有证据支持的结论：BlockStore 日志写入和 bbolt 数据页写入位于块推进的串行关键路径，并出现明显耗时或尖峰；不能把这些区间说成业务规则执行慢或固定共识倒计时。这个结论不代表全部共识延迟都由数据库解释，也未认定硬件、GC或同盘竞争的最终因果。

最小下一步是对同一个 journal 批次记录底层 Write 的次数、实际字节、累计耗时和最大耗时，并给 bbolt 页写循环补同样指标。若时间在多次小 Write 中，才对同一批次做合并写入的离线对照；若在调用之外，使用一个节点的短 runtime trace 区分 CRC/复制、GC 与调度。保持同一写集、原同步规则和正确性检查，再决定是否值得接入完整系统。差值首先叫“未归类墙钟”，不能未经 CPU/运行时证据就叫 CPU 时间。

本轮不改数据库、不削弱 Sync、不增加委员会 writer，也不把依赖提交跨越到后台。应用已经整块原子提交；BlockStore → EndHeight WAL、Finalize 响应 → App.Commit → 共识状态的恢复顺序继续保留。

## 数据与复现

- `prepare.py`、`run_series.py`：第一组 overlay、测试、二进制准备与执行。
- `prepare_leveldb.py`、`run_leveldb.py`：额外的 LevelDB 细分，仍保留全部同步调用。
- `analyze.py`、`detail.py`、`profile.py`、`leveldb_profile.py`：阶段、块间推进与物理提交分析。
- `persist20/`、`persist200/`：逐笔/逐块 CSV、统计 JSON 和审计；`persistence-blocks.csv` 保留四委员的原始块样本。
- `persist200-leveldb/leveldb-blocks.csv`、`leveldb-detail.json`：追加轮的 136 个同步批次及拆分；时间线起点为微秒、elapsed 为单调纳秒，匹配仅允许 2 微秒量化误差。曾发现子调用重构结束比父调用多 291 ns，已修正关联口径，而非丢弃样本。
- 完整 timeline、settlement、私有依赖副本和独立实验数据库保留在 Mac 同名实验目录及 `.run/group-cp-*`；紧凑导出不包含私钥或数据库。

三轮合计 12,003 笔（含预热），均完成，四委员各轮内一致、无资金缺口、pending 排空。追加轮进度查询有 493 次错误，最终任务无失败。诊断版本的 store/committee/requesttrace race 测试通过，LevelDB 追加诊断的 committee race 测试通过。完整原始跟踪留在 Mac，正常实验网络使用原二进制和原数据恢复。

本轮只是诊断，不宣称已优化、持续吞吐上限或长期无积压。
