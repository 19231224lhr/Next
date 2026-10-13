# 长链时间拆分与剩余瓶颈

Stage 9b 的 1000 笔正式数据表明，不能把全部差距归为收证或服务端执行。`stage9b-long-confirm7triples/timing-breakdown.json` 对每个 run 的发送/接收时间轴核对了

`总链时间 = 单笔 receipt 时间之和 + 相邻交易间的间隔之和`。

每个 run 的误差小于 0.01 ms。首笔构造发生在两个系统的链计时之前，因此 preparation 分解均排除首笔。下面是各指标跨七轮的独立中位数，**不能横向相加，也不能用这些中位数相减作精确因果归因**。

| 指标（ms） | Stage 9b | 调优 FastPay |
|---|---:|---:|
| 总链时间 | 6483.819 | 6412.225 |
| 1000 笔 receipt 时间之和 | 5928.600 | 6319.103 |
| 后续 999 笔 request build 之和 | 553.772 | 90.034 |
| 最慢十笔 receipt 时间之和 | 526.883 | 87.456 |

C3 的 receipt 总和中位数较小，后续请求准备时间却明显更大；极慢单笔的累积量也更大。两类问题同时存在。其 P50 优势并不排除长尾，而且较小 receipt 总和并不保证较小整条串链时间。

## 已有诊断的 preparation 占比

轻量读取了两个既有原生报告：

- `/home/qyinf/next-c3-speed-20261014/stage8-long-diagnostic/chain/reports/chain-v4.json`
- `/home/qyinf/next-c3-speed-20261014/stage9-long-diagnostic/chain/reports/chain-v4.json`

两者都有完整的 1000 hop `BuildStagesMS`、`BuildSaveStagesMS`。下表对 hop 2..1000 的**同一个诊断 run**分别求和；占比以该 run 的后续请求 `BuildMS` 总和为分母。它们是 wall-clock、带诊断的 Stage 8/9 数据，不是最终 9b 正式轮数的 CPU 占比。

| 请求准备阶段 | Stage 8 总和（ms）/占比 | Stage 9 总和（ms）/占比 | 实际包围的工作 |
|---|---:|---:|---|
| 读取 coin | 62.879 / 11.61% | 62.331 / 10.71% | wallet Store.View 与完整 DirectCoin JSON 读取 |
| 组装 body | 18.869 / 3.48% | 29.770 / 5.11% | input/proofs、body、grant/admission 与 nonce 组装 |
| 生成 transaction | 138.680 / 25.61% | 146.477 / 25.16% | `bindC3`、`NewFastTx`，包括生成 funding commitment/opening |
| 签名阶段 | 37.243 / 6.88% | 39.747 / 6.83% | TxID 计算与 `SignOwner`，不是单独 Ed25519 的纯 CPU 时间 |
| 保存 request | 274.517 / 50.69% | 294.277 / 50.54% | policy/auth/编码与钱包 Update、预留、journal 提交 |
| 后续 encode marker | 0.354 / 0.07% | 0.368 / 0.06% | 真正编码已在 `SaveDirectRequestBytes` 内；该 marker 是微小的后续 bookkeeping |
| 记录标识 | 8.693 / 1.61% | 8.887 / 1.53% | TxID 等标识记录 |
| BuildMS 总和 | **541.583** | **582.237** | 排除首笔 |

各 marker 的总和与 BuildMS 差约 0.347 / 0.379 ms，来自计时范围边缘的少量 bookkeeping。不能把名称为 `encode_request` 的 0.06% 错读为认证/序列化成本只有这么多。

`save_request` 的进一步划分如下。百分比仍针对整个请求准备总和；`update_total` 是 queue/callback/commit 三项之和，不另加一次。

| 保存内部阶段 | Stage 8（ms）/整体 build 占比 | Stage 9（ms）/整体 build 占比 |
|---|---:|---:|
| encode_before_update | 91.870 / 16.96% | 99.103 / 17.02% |
| queue_wait | 36.982 / 6.83% | 49.380 / 8.48% |
| validation_and_locks | 70.319 / 12.98% | 69.293 / 11.90% |
| commit_and_resume | 75.346 / 13.91% | 76.501 / 13.14% |

`encode_before_update` 覆盖 Update 前的 policy/auth/编码；callback 包括 descriptors、已有 journal/replay、完整 coin 的 Recovered 检查、spend reservation 和写入构造。该采样没有把其中的 coin 解码进一步单独计时，不能把 callback 的全部时间都归为 JSON。`commit_and_resume` 包含 store 提交、唤醒与调度恢复，不是纯磁盘写入或 fsync；实验的钱包仍为 NoSync。

平均队列等待不是最大的准备分量，但有真实的个别尖峰：Stage 9 hop 249 的 BuildMS 为 15.945 ms，其中 queue_wait 15.459 ms；hop 228 的 BuildMS 5.517 ms，其中 commit_and_resume 5.074 ms。Stage 8 hop 965 的 BuildMS 7.370 ms，其中 queue_wait 4.196 ms。它们说明准备尾部会受钱包 writer/恢复等待影响，不能据此推断每个慢 receipt 都有同一原因。

## CPU 证据的范围与下一步判断

这两个最近的 long-diagnostic 目录已有服务端 pprof 文字，但未发现可直接读取的**同轮 client pprof 文字**；本次没有运行 Go pprof，也没有新增任何测量。

已有历史文件 `docs/experiments/preparation-profile-2026-10-13/results/client-cpu-top.txt` 是另一版本、另一次 1.92 s 客户端样本。其累计 CPU 指向后台 `observeChainHop → sourceObserved → wallet.DirectFinal → DirectCoin JSON`：DirectFinal 640/900 ms，JSON Unmarshal 510/900 ms。这能支持“客户端后台观察也消耗 CPU、可能与前台竞争”的假设，**不能作为当前 Stage 9b 前台构造的 CPU 占比**；这些累计项相互包含，不能相加。

现有源码和时间 marker 可确认生成事务约占准备时间四分之一、SaveDirectRequestBytes 约占一半，单独签名阶段不足 7%。`NewFastTx` 包含 RSA chameleon commitment 的随机 opening、模幂与 full-domain hashing；Update 前保存路径还做认证和编码。因此，只优化签名或只缩短 writer queue，均没有证据能消除大部分 preparation 差距。

后续值得验证的是生成/认证/编码中的重复工作，以及完整 coin/journal 解析和后台观察竞争；所有复用必须保留 mutable input ownership、malformed-field 拒绝和钱包 journal 提交边界。当前没有新受控实验把其纯 CPU 时间与调度等待分开，不能给出“去掉某操作即可领先 FastPay”的收益承诺。收证长尾仍要独立研究；请求准备差距不能替代那条证据链。

本文件只分析已保留的实测数据。正式混合实验持续运行期间，未运行测量、构建、测试或 pprof。
